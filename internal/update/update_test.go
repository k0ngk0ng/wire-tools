package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func packageFixture(t *testing.T, entries map[string]string, version string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	name := "wirectl-" + version + "-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz"
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	tw := tar.NewWriter(gz)
	hashes := map[string]string{}
	for n, data := range entries {
		sum := sha256.Sum256([]byte(data))
		hashes[n] = hex.EncodeToString(sum[:])
	}
	metadata, _ := json.Marshal(map[string]any{"name": "wirectl", "version": version, "platform": runtime.GOOS + "-" + runtime.GOARCH, "files": hashes})
	all := map[string]string{}
	for n, d := range entries {
		all[n] = d
	}
	all["wirectl-package.json"] = string(metadata)
	for n, data := range all {
		if err := tw.WriteHeader(&tar.Header{Name: n, Size: int64(len(data)), Mode: 0755, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(data))
	}
	tw.Close()
	gz.Close()
	archive := filepath.Join(dir, name)
	os.WriteFile(archive, compressed.Bytes(), 0600)
	sum := sha256.Sum256(compressed.Bytes())
	checksums := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(checksums, []byte(fmt.Sprintf("%x  %s\n", sum, name)), 0600)
	return archive, checksums
}
func TestOfflineTransactionAndImmutableDependencies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar fixture; Windows real zip exercised by package E2E")
	}
	install := t.TempDir()
	os.MkdirAll(filepath.Join(install, "bin"), 0700)
	executable := filepath.Join(install, "bin", "wirectl")
	os.WriteFile(executable, []byte("old"), 0755)
	archive, sums := packageFixture(t, map[string]string{"bin/wirectl": "new", "libexec/wirectl-download/bin/aria2c": "engine"}, "9.8.7")
	result, err := Run(context.Background(), Options{Executable: executable, Archive: archive, Checksums: sums})
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != "9.8.7" {
		t.Fatal(result)
	}
	data, _ := os.ReadFile(executable)
	if string(data) != "new" {
		t.Fatalf("executable %q", data)
	}
	data, err = os.ReadFile(filepath.Join(install, ".wirectl", "versions", "9.8.7", "libexec", "wirectl-download", "bin", "aria2c"))
	if err != nil || string(data) != "engine" {
		t.Fatalf("bundle missing: %q %v", data, err)
	}
}
func TestRejectBeforeChangingInstallation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tar fixture")
	}
	for _, scenario := range []string{"checksum", "traversal", "missing-executable", "wrong-version", "duplicate-checksum"} {
		t.Run(scenario, func(t *testing.T) {
			install := t.TempDir()
			exe := filepath.Join(install, "wirectl")
			os.WriteFile(exe, []byte("original"), 0755)
			entries := map[string]string{"bin/wirectl": "new"}
			if scenario == "traversal" {
				entries["../escaped"] = "bad"
			}
			if scenario == "missing-executable" {
				delete(entries, "bin/wirectl")
			}
			archive, sums := packageFixture(t, entries, "1.2.3")
			if scenario == "checksum" {
				os.WriteFile(sums, []byte(strings.Repeat("0", 64)+"  "+filepath.Base(archive)), 0600)
			}
			if scenario == "duplicate-checksum" {
				b, _ := os.ReadFile(sums)
				os.WriteFile(sums, append(b, b...), 0600)
			}
			opts := Options{Executable: exe, Archive: archive, Checksums: sums}
			if scenario == "wrong-version" {
				opts.Version = "2.0.0"
			}
			if _, err := Run(context.Background(), opts); err == nil {
				t.Fatal("invalid package accepted")
			}
			data, _ := os.ReadFile(exe)
			if string(data) != "original" {
				t.Fatal("installation changed on rejection")
			}
		})
	}
}
