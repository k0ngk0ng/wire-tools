//go:build windows

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledWindowsEngineExecutableSuffix(t *testing.T) {
	bundle := t.TempDir()
	bin := filepath.Join(bundle, "libexec", "wirectl-download", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRECTL_BUNDLE_DIR", bundle)
	for _, name := range []string{"aria2c", "deno"} {
		path := filepath.Join(bin, name+".exe")
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := ResolveBinary(name); got != path {
			t.Fatalf("ResolveBinary(%q)=%q, want %q", name, got, path)
		}
		if got := ResolveBinary(name + ".exe"); got != path {
			t.Fatalf("explicit exe suffix resolved to %q", got)
		}
	}
}
