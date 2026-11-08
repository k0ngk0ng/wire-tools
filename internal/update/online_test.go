package update

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Failure scenarios are defined before changing the online implementation:
// browser download EOF; missing/nonpositive asset IDs; missing, foreign or
// mismatched REST URLs; changed browser origin/tag; duplicate names; non-octet
// downloads; unsafe redirects; each of the three independent digest failures.
// The fixture uses real release archives and the real install transaction.
type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type onlineFixture struct {
	t             *testing.T
	name, target  string
	archive, sums []byte
	assets        []map[string]any
	requests      []string
	assetRequests map[string]int
	redirect      string
}

func newOnlineFixture(t *testing.T) *onlineFixture {
	t.Helper()
	const version = "9.8.6"
	f := &onlineFixture{t: t, assetRequests: map[string]int{}}
	f.name = "wirectl-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + extension()
	entries := map[string]string{"bin/wirectl" + exeSuffix(): "new from official asset API", "libexec/wirectl-download/proof": "verified runtime"}
	if runtime.GOOS == "windows" {
		var compressed bytes.Buffer
		writer := zip.NewWriter(&compressed)
		hashes := map[string]string{}
		for name, data := range entries {
			hashes[name] = digest([]byte(data))
		}
		meta, _ := json.Marshal(metadata{Name: "wirectl", Version: version, Platform: runtime.GOOS + "-" + runtime.GOARCH, Files: hashes})
		entries["wirectl-package.json"] = string(meta)
		for name, data := range entries {
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			header.SetMode(0755)
			part, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write([]byte(data)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		f.archive = compressed.Bytes()
		f.sums = []byte(digest(f.archive) + "  " + f.name + "\n")
	} else {
		archive, sums := packageFixture(t, entries, version)
		var err error
		f.archive, err = os.ReadFile(archive)
		if err != nil {
			t.Fatal(err)
		}
		f.sums, err = os.ReadFile(sums)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := "https://github.com/" + repository + "/releases/download/v" + version + "/"
	f.assets = []map[string]any{
		{"id": 101, "name": f.name, "url": "https://api.github.com/repos/" + repository + "/releases/assets/101", "browser_download_url": base + f.name, "digest": "sha256:" + digest(f.archive), "size": len(f.archive)},
		{"id": 102, "name": "SHA256SUMS", "url": "https://api.github.com/repos/" + repository + "/releases/assets/102", "browser_download_url": base + "SHA256SUMS", "digest": "sha256:" + digest(f.sums), "size": len(f.sums)},
	}
	install := t.TempDir()
	f.target = filepath.Join(install, "bin", "wirectl"+exeSuffix())
	if err := os.MkdirAll(filepath.Dir(f.target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.target, []byte("original installation"), 0755); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = updateRoundTrip(f.roundTrip)
	t.Cleanup(func() { http.DefaultTransport = previous })
	return f
}

func fixtureResponse(r *http.Request, status int, data []byte, location string) *http.Response {
	headers := make(http.Header)
	if location != "" {
		headers.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: headers, ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data)), Request: r}
}
func (f *onlineFixture) roundTrip(r *http.Request) (*http.Response, error) {
	address := r.URL.String()
	f.requests = append(f.requests, address)
	switch address {
	case "https://api.github.com/repos/" + repository + "/releases/latest":
		data, err := json.Marshal(map[string]any{"tag_name": "v9.8.6", "draft": false, "assets": f.assets})
		if err != nil {
			return nil, err
		}
		return fixtureResponse(r, 200, data, ""), nil
	case "https://api.github.com/repos/" + repository + "/releases/assets/101", "https://api.github.com/repos/" + repository + "/releases/assets/102":
		f.assetRequests[address]++
		if r.Header.Get("Accept") != "application/octet-stream" {
			return fixtureResponse(r, 400, []byte("asset API requires application/octet-stream"), ""), nil
		}
		location := "https://release-assets.githubusercontent.com/fixture/archive"
		if strings.HasSuffix(address, "/102") {
			location = "https://release-assets.githubusercontent.com/fixture/sums"
		}
		if f.redirect != "" {
			location = f.redirect
		}
		return fixtureResponse(r, 302, nil, location), nil
	case "https://release-assets.githubusercontent.com/fixture/archive":
		return fixtureResponse(r, 200, f.archive, ""), nil
	case "https://release-assets.githubusercontent.com/fixture/sums":
		return fixtureResponse(r, 200, f.sums, ""), nil
	}
	if r.URL.Hostname() == "github.com" {
		// Reproduces the deployed 1.0.2 browser-download entry point failure.
		return nil, io.ErrUnexpectedEOF
	}
	return nil, fmt.Errorf("fixture refused unexpected network request: %s", address)
}
func (f *onlineFixture) unchanged(t *testing.T) {
	t.Helper()
	original, err := os.ReadFile(f.target)
	if err != nil || string(original) != "original installation" {
		t.Fatalf("installation changed after rejection: %q %v", original, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(f.target)), ".wirectl", "versions", "9.8.6")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected update installed a generation: %v", err)
	}
}

func TestOnlineUpdateUsesOfficialAssetAPIWhenBrowserRouteFails(t *testing.T) {
	f := newOnlineFixture(t)
	result, err := Run(context.Background(), Options{Executable: f.target})
	if err != nil {
		t.Fatalf("official REST asset flow should bypass browser EOF: %v", err)
	}
	if result.Version != "9.8.6" {
		t.Fatal(result)
	}
	for _, id := range []string{"101", "102"} {
		address := "https://api.github.com/repos/" + repository + "/releases/assets/" + id
		if f.assetRequests[address] != 1 {
			t.Fatalf("REST asset request %s count %d", id, f.assetRequests[address])
		}
	}
	for _, address := range f.requests {
		if strings.HasPrefix(address, "https://github.com/") {
			t.Fatalf("browser route used: %s", address)
		}
	}
	binary, err := os.ReadFile(f.target)
	if err != nil || string(binary) != "new from official asset API" {
		t.Fatalf("new executable missing: %q %v", binary, err)
	}
}

func TestOnlineUpdateRejectsUnboundAssetMetadataBeforeDownload(t *testing.T) {
	scenarios := map[string]func(map[string]any){
		"missing-id":         func(a map[string]any) { delete(a, "id") },
		"zero-id":            func(a map[string]any) { a["id"] = 0 },
		"negative-id":        func(a map[string]any) { a["id"] = -1 },
		"non-integer-id":     func(a map[string]any) { a["id"] = 1.5 },
		"missing-api-url":    func(a map[string]any) { delete(a, "url") },
		"foreign-repository": func(a map[string]any) { a["url"] = "https://api.github.com/repos/other/project/releases/assets/101" },
		"mismatched-id": func(a map[string]any) {
			a["url"] = "https://api.github.com/repos/" + repository + "/releases/assets/999"
		},
		"api-query": func(a map[string]any) { a["url"] = a["url"].(string) + "?download=1" },
		"api-http":  func(a map[string]any) { a["url"] = strings.Replace(a["url"].(string), "https:", "http:", 1) },
		"wrong-browser-origin": func(a map[string]any) {
			a["browser_download_url"] = strings.Replace(a["browser_download_url"].(string), "github.com", "untrusted.invalid", 1)
		},
		"wrong-name": func(a map[string]any) { a["name"] = "unexpected-asset" },
		"wrong-browser-tag": func(a map[string]any) {
			a["browser_download_url"] = strings.Replace(a["browser_download_url"].(string), "v9.8.6", "v9.8.5", 1)
		},
	}
	for name, change := range scenarios {
		for _, index := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/asset-%d", name, index), func(t *testing.T) {
				f := newOnlineFixture(t)
				change(f.assets[index])
				if _, err := Run(context.Background(), Options{Executable: f.target}); err == nil {
					t.Fatal("unbound asset metadata accepted")
				}
				if len(f.requests) != 1 {
					t.Fatalf("invalid metadata triggered download requests: %v", f.requests)
				}
				f.unchanged(t)
			})
		}
	}
}
func TestOnlineAssetAPIPreservesAllDigestChecks(t *testing.T) {
	for _, scenario := range []string{"archive-github-digest", "sums-github-digest", "independent-checksum"} {
		t.Run(scenario, func(t *testing.T) {
			f := newOnlineFixture(t)
			switch scenario {
			case "archive-github-digest":
				f.assets[0]["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "sums-github-digest":
				f.assets[1]["digest"] = "sha256:" + strings.Repeat("0", 64)
			case "independent-checksum":
				f.sums = []byte(strings.Repeat("0", 64) + "  " + f.name + "\n")
				f.assets[1]["digest"] = "sha256:" + digest(f.sums)
			}
			if _, err := Run(context.Background(), Options{Executable: f.target}); err == nil {
				t.Fatal("digest failure accepted")
			}
			if len(f.assetRequests) == 0 {
				t.Fatal("test did not reach official REST asset download")
			}
			f.unchanged(t)
		})
	}
}
func TestOnlineAssetAPIRejectsUnsafeRedirects(t *testing.T) {
	for _, location := range []string{"https://untrusted.invalid/archive", "http://release-assets.githubusercontent.com/archive"} {
		t.Run(location, func(t *testing.T) {
			f := newOnlineFixture(t)
			f.redirect = location
			if _, err := Run(context.Background(), Options{Executable: f.target}); err == nil || !strings.Contains(err.Error(), "redirect") {
				t.Fatalf("unsafe redirect was not rejected: %v", err)
			}
			for _, address := range f.requests {
				if address == location {
					t.Fatalf("unsafe destination was requested: %s", address)
				}
			}
			f.unchanged(t)
		})
	}
}

func TestOnlineUpdateRejectsDuplicateAssetNames(t *testing.T) {
	f := newOnlineFixture(t)
	f.assets = append(f.assets, f.assets[0])
	if _, err := Run(context.Background(), Options{Executable: f.target}); err == nil {
		t.Fatal("duplicate asset accepted")
	}
	if len(f.requests) != 1 {
		t.Fatalf("duplicate metadata triggered downloads: %v", f.requests)
	}
	f.unchanged(t)
}
