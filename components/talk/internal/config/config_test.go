package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitProtectsExistingRoom(t *testing.T) {
	dir := t.TempDir()
	a, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err = Save(dir, a); err != nil {
		t.Fatal(err)
	}
	b, _ := New()
	if err = Save(dir, b); err == nil {
		t.Fatal("overwrote room")
	}
	got, err := Load(dir)
	if err != nil || got.Key != a.Key {
		t.Fatal(got, err)
	}
	if _, err = os.Stat(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
}
