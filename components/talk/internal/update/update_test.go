package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func packageBytes(t *testing.T, name string, mode byte, payload []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if runtime.GOOS == "windows" {
		z := zip.NewWriter(&b)
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0755)
		if mode == tar.TypeSymlink {
			h.SetMode(os.ModeSymlink | 0755)
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(payload)
		z.Close()
	} else {
		gz := gzip.NewWriter(&b)
		tw := tar.NewWriter(gz)
		h := &tar.Header{Name: name, Mode: 0755, Size: int64(len(payload)), Typeflag: mode}
		if mode == tar.TypeSymlink {
			h.Size = 0
			h.Linkname = "outside"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if mode != tar.TypeSymlink {
			tw.Write(payload)
		}
		tw.Close()
		gz.Close()
	}
	return b.Bytes()
}
func TestOfflineUpdateVerifiesBeforeReplacement(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "installed")
	os.WriteFile(target, []byte("old"), 0755)
	name := "wire-talk-0.1.0-" + runtime.GOOS + "-" + runtime.GOARCH + extension()
	binary := "bin/wirectl-talk"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	data := packageBytes(t, binary, tar.TypeReg, []byte("replacement"))
	archive := filepath.Join(dir, name)
	sums := filepath.Join(dir, "SHA256SUMS")
	os.WriteFile(archive, data, 0600)
	os.WriteFile(sums, []byte(strings.Repeat("0", 64)+"  "+name), 0600)
	options := Options{Executable: target, Archive: archive, Checksums: sums}
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("accepted wrong checksum")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "old" {
		t.Fatal("modified executable before verification")
	}
	hash := sha256.Sum256(data)
	os.WriteFile(sums, []byte(fmt.Sprintf("%x  %s\n", hash, name)), 0600)
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(target)
	if string(b) != "replacement" {
		t.Fatal("did not install replacement")
	}
	os.WriteFile(filepath.Join(dir, ".wire-talk-package-manager"), []byte("homebrew"), 0600)
	if _, err := Run(context.Background(), options); err == nil {
		t.Fatal("modified package-managed installation")
	}
}
func TestArchiveRejectsTraversalAndLinks(t *testing.T) {
	for _, entry := range []struct {
		name string
		mode byte
	}{{"../bin/wirectl-talk", tar.TypeReg}, {"/bin/wirectl-talk", tar.TypeReg}, {"bin/wirectl-talk", tar.TypeSymlink}, {"a/../../bin/wirectl-talk", tar.TypeReg}} {
		data := packageBytes(t, entry.name, entry.mode, []byte("bad"))
		if _, err := extract("test"+extension(), data); err == nil {
			t.Fatalf("accepted %s", entry.name)
		}
	}
}
func TestChecksumsRejectDuplicate(t *testing.T) {
	data := []byte("test")
	sum := sha256.Sum256(data)
	line := fmt.Sprintf("%x  archive\n", sum)
	if err := verifyChecksums("archive", data, []byte(line+line)); err == nil {
		t.Fatal("accepted duplicate checksum")
	}
}
