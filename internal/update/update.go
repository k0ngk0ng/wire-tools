// Package update installs a verified release as one versioned runtime bundle.
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
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/internal/installpath"
	"github.com/k0ngk0ng/wire-tools/internal/state"
)

const repository = "k0ngk0ng/wire-tools"
const archiveLimit int64 = 768 << 20
const expandedLimit int64 = 2 << 30

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

type Options struct{ Version, Archive, Checksums, Executable string }
type Result struct {
	Version        string
	PackageManager string
}
type asset struct {
	ID     int64  `json:"id"`
	Size   int64  `json:"size"`
	APIURL string `json:"url"`
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}
type release struct {
	Tag    string  `json:"tag_name"`
	Draft  bool    `json:"draft"`
	Assets []asset `json:"assets"`
}
type metadata struct {
	Name     string            `json:"name"`
	Version  string            `json:"version"`
	Platform string            `json:"platform"`
	Files    map[string]string `json:"files"`
}

func Run(ctx context.Context, o Options) (Result, error) {
	target := o.Executable
	var err error
	if target == "" {
		target, err = os.Executable()
		if err != nil {
			return Result{}, err
		}
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return Result{}, err
	}
	target, err = installpath.Resolve(target)
	if err != nil {
		return Result{}, err
	}
	st, err := os.Stat(target)
	if err != nil {
		return Result{}, err
	}
	if !st.Mode().IsRegular() {
		return Result{}, errors.New("installation executable must be a regular file")
	}
	if marker, err := os.ReadFile(filepath.Join(filepath.Dir(target), ".wirectl-package-manager")); err == nil {
		if o.Archive != "" || o.Checksums != "" || o.Version != "" {
			return Result{}, errors.New("version and offline overrides are unavailable for package-managed installations")
		}
		manager := strings.TrimSpace(string(marker))
		var commands []*exec.Cmd
		switch manager {
		case "homebrew":
			commands = []*exec.Cmd{
				exec.CommandContext(ctx, "brew", "update"),
				exec.CommandContext(ctx, "brew", "upgrade", "k0ngk0ng/wire-tools/wirectl"),
			}
		case "scoop":
			if runtime.GOOS == "windows" {
				commands = []*exec.Cmd{
					exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "$ErrorActionPreference = 'Stop'; & scoop update; if (-not $?) { exit 1 }; exit 0"),
					exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", "$ErrorActionPreference = 'Stop'; & scoop update wirectl; if (-not $?) { exit 1 }; exit 0"),
				}
			} else {
				commands = []*exec.Cmd{
					exec.CommandContext(ctx, "scoop", "update"),
					exec.CommandContext(ctx, "scoop", "update", "wirectl"),
				}
			}
		default:
			return Result{}, errors.New("unrecognized package manager marker")
		}
		for _, cmd := range commands {
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := cmd.Run(); err != nil {
				return Result{}, err
			}
		}
		return Result{Version: "latest", PackageManager: manager}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}
	platform := runtime.GOOS + "-" + runtime.GOARCH
	switch platform {
	case "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64":
	default:
		return Result{}, fmt.Errorf("no published release for %s", platform)
	}
	var data, sums []byte
	var name, version string
	if o.Archive != "" || o.Checksums != "" {
		if o.Archive == "" || o.Checksums == "" || o.Version != "" {
			return Result{}, errors.New("offline update requires --archive and --checksums, without --version")
		}
		name = filepath.Base(o.Archive)
		suffix := "-" + platform + extension()
		if !strings.HasPrefix(name, "wirectl-") || !strings.HasSuffix(name, suffix) {
			return Result{}, errors.New("archive does not match this platform")
		}
		version = strings.TrimSuffix(strings.TrimPrefix(name, "wirectl-"), suffix)
		if !semver.MatchString(version) {
			return Result{}, errors.New("invalid archive version")
		}
		data, err = readFile(o.Archive, archiveLimit)
		if err != nil {
			return Result{}, err
		}
		sums, err = readFile(o.Checksums, 2<<20)
		if err != nil {
			return Result{}, err
		}
	} else {
		endpoint := "https://api.github.com/repos/" + repository + "/releases/latest"
		if o.Version != "" {
			version = strings.TrimPrefix(o.Version, "v")
			if !semver.MatchString(version) {
				return Result{}, errors.New("invalid semantic version")
			}
			endpoint = "https://api.github.com/repos/" + repository + "/releases/tags/v" + version
		}
		raw, err := fetch(ctx, endpoint, 2<<20)
		if err != nil {
			return Result{}, err
		}
		var rel release
		if err = json.Unmarshal(raw, &rel); err != nil {
			return Result{}, err
		}
		version = strings.TrimPrefix(rel.Tag, "v")
		if rel.Draft || rel.Tag != "v"+version || !semver.MatchString(version) {
			return Result{}, errors.New("invalid GitHub release metadata")
		}
		name = "wirectl-" + version + "-" + platform + extension()
		lookup := func(want string) (asset, error) {
			var found asset
			count := 0
			for _, a := range rel.Assets {
				if a.Name == want {
					found = a
					count++
				}
			}
			if count != 1 || found.URL != "https://github.com/"+repository+"/releases/download/"+rel.Tag+"/"+want {
				return found, errors.New("unexpected or duplicate GitHub release asset")
			}
			if found.ID <= 0 || found.APIURL != fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", repository, found.ID) {
				return found, errors.New("invalid GitHub release asset REST URL or ID")
			}
			return found, nil
		}
		a, err := lookup(name)
		if err != nil {
			return Result{}, err
		}
		s, err := lookup("SHA256SUMS")
		if err != nil {
			return Result{}, err
		}
		data, err = downloadArchive(ctx, a)
		if err != nil {
			return Result{}, err
		}
		if err = verifyDigest(data, a.Digest); err != nil {
			return Result{}, err
		}
		sums, err = fetchAsset(ctx, s.APIURL, 2<<20)
		if err != nil {
			return Result{}, err
		}
		if err = verifyDigest(sums, s.Digest); err != nil {
			return Result{}, err
		}
	}
	if err = verifyChecksums(name, data, sums); err != nil {
		return Result{}, err
	}
	root := filepath.Dir(target)
	if filepath.Base(root) == "bin" {
		root = filepath.Dir(root)
	}
	versions := filepath.Join(root, ".wirectl", "versions")
	if err = state.Dir(versions); err != nil {
		return Result{}, err
	}
	unlock, err := state.Lock(filepath.Join(root, ".wirectl", "update"))
	if err != nil {
		return Result{}, fmt.Errorf("another update is active: %w", err)
	}
	defer unlock()
	stage, err := os.MkdirTemp(versions, ".staging-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(stage)
	if err = extract(name, data, stage, version, platform); err != nil {
		return Result{}, err
	}
	bundle := filepath.Join(versions, version)
	// A previously installed immutable generation is reused only when every
	// package byte matches. Never rewrite files loaded by a running process.
	if _, err = os.Lstat(bundle); err == nil {
		if err = equalBundle(stage, bundle); err != nil {
			return Result{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	} else if err = os.Rename(stage, bundle); err != nil {
		return Result{}, err
	}
	source := filepath.Join(bundle, "bin", "wirectl"+exeSuffix())
	binary, err := readFile(source, 128<<20)
	if err != nil {
		return Result{}, err
	}
	staged, err := os.CreateTemp(filepath.Dir(target), ".wirectl-update-*")
	if err != nil {
		return Result{}, err
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if err = staged.Chmod(st.Mode().Perm() | 0100); err != nil {
		staged.Close()
		return Result{}, err
	}
	if _, err = staged.Write(binary); err != nil {
		staged.Close()
		return Result{}, err
	}
	if err = staged.Sync(); err != nil {
		staged.Close()
		return Result{}, err
	}
	if err = staged.Close(); err != nil {
		return Result{}, err
	}
	if err = replace(stagedPath, target); err != nil {
		return Result{}, err
	}
	return Result{Version: version}, nil
}
func extension() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
func readFile(name string, limit int64) ([]byte, error) {
	st, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("update input must be a bounded regular file")
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return bounded(f, limit)
}
func bounded(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("update size limit exceeded")
	}
	return b, err
}
func allowed(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	switch u.Hostname() {
	case "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	}
	return false
}
func fetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	return fetchWithAccept(ctx, address, limit, "application/vnd.github+json")
}
func fetchAsset(ctx context.Context, address string, limit int64) ([]byte, error) {
	return fetchWithAccept(ctx, address, limit, "application/octet-stream")
}
func fetchWithAccept(ctx context.Context, address string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	if !allowed(req.URL) {
		return nil, errors.New("untrusted update URL")
	}
	req.Header.Set("User-Agent", "wirectl-updater")
	req.Header.Set("Accept", accept)
	client := &http.Client{Timeout: 5 * time.Minute, CheckRedirect: updateRedirect}
	assetRequest := accept == "application/octet-stream"
	if assetRequest {
		client = newAssetClient(5 * time.Minute)
		defer closeAssetClient(client)
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("Cache-Control", "no-cache")
	}
	resp, err := client.Do(req)
	if err != nil {
		if assetRequest {
			return nil, safeAssetError(err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		if assetRequest {
			return nil, fmt.Errorf("release asset HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("GitHub release: %s", resp.Status)
	}
	data, err := bounded(resp.Body, limit)
	if assetRequest && err != nil {
		return nil, safeAssetError(err)
	}
	return data, err
}
func digest(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func verifyDigest(data []byte, expected string) error {
	if expected != "sha256:"+digest(data) {
		return errors.New("GitHub asset SHA-256 missing or mismatched")
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
	if found != digest(data) {
		return errors.New("archive SHA256SUMS mismatch or missing entry")
	}
	return nil
}
func extract(name string, data []byte, stage, version, platform string) error {
	seen := map[string]bool{}
	hashes := map[string]string{}
	entries := 0
	var total int64
	take := func(name string, mode os.FileMode, size int64, reader io.Reader) error {
		entries++
		total += size
		if entries > 10000 || size < 0 || size > expandedLimit || total > expandedLimit {
			return errors.New("expanded archive exceeds limits")
		}
		clean := strings.TrimSuffix(name, "/")
		if clean == "" || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || path.Clean(clean) != clean || strings.ContainsAny(clean, "\\:\x00") || strings.HasPrefix(clean, "/") || seen[clean] {
			return errors.New("unsafe or duplicate archive entry")
		}
		seen[clean] = true
		destination := filepath.Join(stage, filepath.FromSlash(clean))
		if mode.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		if !mode.IsRegular() {
			return errors.New("archive contains a link or special file")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		f, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm()&0777)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(reader, size+1))
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if n != size {
			return errors.New("truncated archive entry")
		}
		hashes[clean] = hex.EncodeToString(h.Sum(nil))
		return nil
	}
	if strings.HasSuffix(name, ".zip") {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, f := range z.File {
			if f.UncompressedSize64 > uint64(expandedLimit) {
				return errors.New("expanded archive exceeds limits")
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = take(f.Name, f.Mode(), int64(f.UncompressedSize64), r)
			r.Close()
			if err != nil {
				return err
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
				return errors.New("archive contains a link or special file")
			}
			if err = take(header.Name, header.FileInfo().Mode(), header.Size, tr); err != nil {
				return err
			}
		}
		if _, err = io.Copy(io.Discard, gz); err != nil {
			return err
		}
	}
	metaBytes, err := readFile(filepath.Join(stage, "wirectl-package.json"), 2<<20)
	if err != nil {
		return fmt.Errorf("release package metadata: %w", err)
	}
	var meta metadata
	if err = json.Unmarshal(metaBytes, &meta); err != nil {
		return err
	}
	if meta.Name != "wirectl" || meta.Version != version || meta.Platform != platform || len(meta.Files) == 0 {
		return errors.New("release package metadata does not match archive")
	}
	delete(hashes, "wirectl-package.json")
	if len(hashes) != len(meta.Files) {
		return errors.New("release package inventory is incomplete")
	}
	for n, sum := range hashes {
		if meta.Files[n] != sum {
			return fmt.Errorf("release package inventory mismatch: %s", n)
		}
	}
	executable := filepath.Join(stage, "bin", "wirectl"+exeSuffix())
	st, err := os.Stat(executable)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return errors.New("release package has no executable")
	}
	if err = os.Chmod(executable, 0755); err != nil {
		return err
	}
	return nil
}
func equalBundle(staged, existing string) error {
	return filepath.WalkDir(staged, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(staged, p)
		if d.IsDir() {
			st, err := os.Lstat(filepath.Join(existing, rel))
			if err != nil || !st.IsDir() {
				return errors.New("existing version bundle is invalid")
			}
			return nil
		}
		a, err := readFile(p, expandedLimit)
		if err != nil {
			return err
		}
		b, err := readFile(filepath.Join(existing, rel), expandedLimit)
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return errors.New("the installed version already exists with different contents")
		}
		return nil
	})
}
