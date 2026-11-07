package runtimefiles

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWintunSelectsCurrentGenerationBeforeAdjacentRuntime(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "bin", "wirectl.exe")
	generation := filepath.Join(root, ".wirectl", "versions", "2.0.0")
	for _, path := range []string{executable, filepath.Join(root, "bin", "wintun.dll"), filepath.Join(generation, "bin", "wintun.dll"), filepath.Join(generation, "bin", "WINTUN-LICENSE.txt")} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dll, license, err := Wintun(executable, generation)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(generation)
	if dll != filepath.Join(canonical, "bin", "wintun.dll") || license != filepath.Join(canonical, "bin", "WINTUN-LICENSE.txt") {
		t.Fatal(dll, license)
	}
	if err := os.Remove(dll); err != nil {
		t.Fatal(err)
	}
	again, _, err := Wintun(executable, generation)
	if err != nil {
		t.Fatal(err)
	}
	if again != dll {
		t.Fatal("missing generation DLL fell back to stale adjacent runtime", again)
	}
}

func TestWintunHelperCopiesIgnoreInheritedGeneration(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "protected-helper", "wirectl-connect.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	dll, license, err := Wintun(executable, filepath.Join(root, "untrusted-generation"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(executable)
	if dll != filepath.Join(filepath.Dir(canonical), "wintun.dll") || license != filepath.Join(filepath.Dir(canonical), "WINTUN-LICENSE.txt") {
		t.Fatal(dll, license)
	}
}

func TestWintunRejectsUntrustedGenerationRoots(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "bin", "wirectl.exe")
	if err := os.MkdirAll(filepath.Dir(executable), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, bundle := range []string{"relative-generation", filepath.Join(root, "elsewhere"), filepath.Join(root, ".wirectl", "versions"), filepath.Join(root, ".wirectl", "versions", "2.0.0", "nested")} {
		if filepath.IsAbs(bundle) {
			if err := os.MkdirAll(bundle, 0700); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := Wintun(executable, bundle); err == nil {
			t.Errorf("accepted %q", bundle)
		}
	}
}
