package update

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScoopJunctionRetainsPackageManagerProtection(t *testing.T) {
	root := t.TempDir()
	version := filepath.Join(root, "1.2.3")
	bin := filepath.Join(version, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "wirectl-talk.exe"), []byte("test"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, ".wire-talk-package-manager"), []byte("scoop\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", current, version).CombinedOutput(); err != nil {
		t.Fatalf("junction: %v: %s", err, out)
	}
	defer os.Remove(current)
	_, err := Run(context.Background(), Options{Executable: filepath.Join(current, "bin", "wirectl-talk.exe")})
	if err == nil || !strings.Contains(err.Error(), "scoop update") {
		t.Fatalf("package protection was bypassed: %v", err)
	}
}
