//go:build !windows

package handset

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// Failure scenarios are listed in docs/INTEGRATION_PLAN.md before implementation.
func TestHandsetFollowsSelectedUSBAudioDevice(t *testing.T) {
	root := t.TempDir()
	sound := filepath.Join(root, "sound")
	usb := []string{filepath.Join(root, "usb", "handset"), filepath.Join(root, "usb", "ringer")}
	for i, path := range usb {
		iface := filepath.Join(path, "audio-interface")
		if err := os.MkdirAll(iface, 0700); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"idVendor", "idProduct"} {
			if err := os.WriteFile(filepath.Join(path, field), []byte("12d1"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		card := filepath.Join(sound, []string{"card1", "card2"}[i])
		if err := os.MkdirAll(card, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(iface, filepath.Join(card, "device")); err != nil {
			t.Fatal(err)
		}
	}
	id := func(s string) string { return hex.EncodeToString([]byte(s)) }
	for _, tc := range []struct {
		name   string
		ids    []string
		want   string
		pinned bool
	}{
		{"input takes precedence", []string{id(":1,0"), id(":2,0")}, usb[0], true},
		{"output when no microphone", []string{"none", id(":2,0")}, usb[1], true},
		{"selected device absent", []string{id(":3,0")}, "", true},
		{"absent input cannot claim ringer", []string{id(":3,0"), id(":2,0")}, "", true},
		{"malformed device", []string{"not-hex"}, "", true},
		{"path traversal", []string{id(":../../etc,0")}, "", true},
		{"negative card", []string{id(":-1,0")}, "", true},
		{"default", []string{"", ""}, "", false},
		{"file input and default output", []string{"none", ""}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, pinned := selectedUSBRoot(tc.ids, sound)
			if got != tc.want || pinned != tc.pinned {
				t.Fatalf("got (%q, %v), want (%q, %v)", got, pinned, tc.want, tc.pinned)
			}
		})
	}
	if err := os.RemoveAll(usb[0]); err != nil {
		t.Fatal(err)
	}
	if got, pinned := selectedUSBRoot([]string{id(":1,0")}, sound); got != "" || !pinned {
		t.Fatalf("broken sysfs link picked another handset: %q, %v", got, pinned)
	}
}
