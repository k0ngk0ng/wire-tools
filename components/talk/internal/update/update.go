// Package update verifies official release checksums before replacing the plugin.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/installpath"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const repository = "k0ngk0ng/wire-talk"
const limit = 128 << 20

var semver = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

type Options struct{ Version, Archive, Checksums, Executable string }
type asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}
type release struct {
	Tag    string  `json:"tag_name"`
	Assets []asset `json:"assets"`
	Draft  bool    `json:"draft"`
}

func Run(ctx context.Context, o Options) (string, error) {
	target := o.Executable
	var err error
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	target, err = installpath.Resolve(target)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(filepath.Join(filepath.Dir(target), ".wire-talk-package-manager")); err == nil {
		return "", errors.New("package-managed installation: use brew upgrade k0ngk0ng/tap/wire-talk or scoop update wire-talk")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	st, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", errors.New("executable is not a regular file")
	}
	var archive, sums []byte
	var name, version string
	if o.Archive != "" || o.Checksums != "" {
		if o.Archive == "" || o.Checksums == "" {
			return "", errors.New("offline update requires both --archive and --checksums")
		}
		if o.Version != "" {
			return "", errors.New("--version cannot be combined with offline update")
		}
		name = filepath.Base(o.Archive)
		archive, err = readFile(o.Archive, limit)
		if err != nil {
			return "", err
		}
		sums, err = readFile(o.Checksums, 2<<20)
		if err != nil {
			return "", err
		}
		suffix := "-" + runtime.GOOS + "-" + runtime.GOARCH + extension()
		if !strings.HasPrefix(name, "wire-talk-") || !strings.HasSuffix(name, suffix) {
			return "", errors.New("archive does not match this platform")
		}
		version = strings.TrimSuffix(strings.TrimPrefix(name, "wire-talk-"), suffix)
		if !semver.MatchString(version) {
			return "", errors.New("invalid archive version")
		}
	} else {
		endpoint := "https://api.github.com/repos/" + repository + "/releases/latest"
		if o.Version != "" {
			if !semver.MatchString(o.Version) {
				return "", errors.New("invalid semantic version")
			}
			endpoint = "https://api.github.com/repos/" + repository + "/releases/tags/v" + strings.TrimPrefix(o.Version, "v")
		}
		b, e := fetch(ctx, endpoint, 2<<20)
		if e != nil {
			return "", e
		}
		var rel release
		if err = json.Unmarshal(b, &rel); err != nil {
			return "", err
		}
		if rel.Draft || !semver.MatchString(rel.Tag) {
			return "", errors.New("invalid release metadata")
		}
		version = strings.TrimPrefix(rel.Tag, "v")
		name = "wire-talk-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + extension()
		lookup := func(want string) (asset, error) {
			var found asset
			count := 0
			for _, a := range rel.Assets {
				if a.Name == want {
					found = a
					count++
				}
			}
			if count != 1 {
				return found, fmt.Errorf("release must contain exactly one %s asset", want)
			}
			expected := "https://github.com/" + repository + "/releases/download/" + rel.Tag + "/" + want
			if found.URL != expected {
				return found, errors.New("unexpected release asset URL")
			}
			return found, nil
		}
		a, e := lookup(name)
		if e != nil {
			return "", e
		}
		s, e := lookup("SHA256SUMS")
		if e != nil {
			return "", e
		}
		archive, err = fetch(ctx, a.URL, limit)
		if err != nil {
			return "", err
		}
		if err = verifyDigest(archive, a.Digest); err != nil {
			return "", err
		}
		sums, err = fetch(ctx, s.URL, 2<<20)
		if err != nil {
			return "", err
		}
		if err = verifyDigest(sums, s.Digest); err != nil {
			return "", err
		}
	}
	if err = verifyChecksums(name, archive, sums); err != nil {
		return "", err
	}
	binary, err := extract(name, archive)
	if err != nil {
		return "", err
	}
	staged, err := os.CreateTemp(filepath.Dir(target), ".talk-update-*")
	if err != nil {
		return "", err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if err = staged.Chmod(st.Mode().Perm()); err != nil {
		staged.Close()
		return "", err
	}
	if _, err = staged.Write(binary); err != nil {
		staged.Close()
		return "", err
	}
	if err = staged.Sync(); err != nil {
		staged.Close()
		return "", err
	}
	if err = staged.Close(); err != nil {
		return "", err
	}
	if err = replace(stagedPath, target); err != nil {
		return "", err
	}
	return version, nil
}
func extension() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}
func readFile(p string, max int64) ([]byte, error) {
	s, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Size() > max {
		return nil, errors.New("update file must be a bounded regular file")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return bounded(f, max)
}
func bounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err == nil && int64(len(b)) > max {
		err = errors.New("update exceeds size limit")
	}
	return b, err
}
func allowedURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func fetch(ctx context.Context, address string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	if !allowedURL(req.URL) {
		return nil, errors.New("untrusted update URL")
	}
	req.Header.Set("User-Agent", "wire-talk-updater")
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || !allowedURL(r.URL) {
			return errors.New("untrusted update redirect")
		}
		return nil
	}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub update: %s", resp.Status)
	}
	return bounded(resp.Body, max)
}
func verifyDigest(data []byte, digest string) error {
	if !strings.HasPrefix(digest, "sha256:") {
		return errors.New("GitHub asset SHA-256 digest is missing")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.TrimPrefix(digest, "sha256:") {
		return errors.New("GitHub asset SHA-256 mismatch")
	}
	return nil
}
func verifyChecksums(name string, data, sums []byte) error {
	found := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if found != "" {
				return errors.New("duplicate archive checksum")
			}
			found = fields[0]
		}
	}
	sum := sha256.Sum256(data)
	if found != hex.EncodeToString(sum[:]) {
		return errors.New("archive SHA256SUMS mismatch or missing entry")
	}
	return nil
}
func extract(name string, data []byte) ([]byte, error) {
	executable := "bin/wirectl-talk"
	if strings.HasSuffix(name, ".zip") {
		executable += ".exe"
	}
	var result []byte
	entries := 0
	var size int64
	seen := map[string]bool{}
	take := func(name string, mode os.FileMode, n int64, r io.Reader) error {
		entries++
		if entries > 128 || n < 0 || n > limit {
			return errors.New("archive limits exceeded")
		}
		size += n
		if size > limit {
			return errors.New("expanded archive too large")
		}
		if name == "" || strings.Contains(name, "\\") || strings.Contains(name, ":") || strings.HasPrefix(name, "/") || path.Clean(name) != strings.TrimSuffix(name, "/") {
			return errors.New("unsafe archive path")
		}
		if name == ".." || strings.HasPrefix(name, "../") || seen[name] {
			return errors.New("unsafe or duplicate archive entry")
		}
		seen[name] = true
		if mode.IsDir() {
			return nil
		}
		if !mode.IsRegular() {
			return errors.New("archive contains a link or special file")
		}
		if name == executable {
			if n == 0 || n > 64<<20 {
				return errors.New("invalid executable size")
			}
			var err error
			result, err = bounded(r, 64<<20)
			return err
		}
		return nil
	}
	if strings.HasSuffix(name, ".zip") {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.UncompressedSize64 > limit {
				return nil, errors.New("expanded archive too large")
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			e = take(f.Name, f.Mode(), int64(f.UncompressedSize64), r)
			r.Close()
			if e != nil {
				return nil, e
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return nil, e
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir {
				return nil, errors.New("archive contains a link or special file")
			}
			if e = take(h.Name, h.FileInfo().Mode(), h.Size, tr); e != nil {
				return nil, e
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("archive has no talk executable")
	}
	return result, nil
}
