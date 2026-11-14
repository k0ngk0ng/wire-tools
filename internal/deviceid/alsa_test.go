//go:build !windows

package deviceid

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestALSAIdentitySurvivesRenumbering(t *testing.T) {
	root := t.TempDir()
	sound := filepath.Join(root, "sound")
	add := func(number, name, product string) string {
		usb := filepath.Join(root, "usb", name)
		iface := filepath.Join(usb, "interface")
		card := filepath.Join(sound, "card"+number)
		for _, p := range []string{iface, card} {
			if err := os.MkdirAll(p, 0700); err != nil {
				t.Fatal(err)
			}
		}
		for file, value := range map[string]string{"idVendor": "12d1", "idProduct": product} {
			if err := os.WriteFile(filepath.Join(usb, file), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(card, "id"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(iface, filepath.Join(card, "device")); err != nil {
			t.Fatal(err)
		}
		return usb
	}
	id := func(s string) string { return hex.EncodeToString([]byte(s)) }
	add("1", "Device", "1203")
	voice := add("2", "Audio", "3a07")
	stableVoice := id(":CARD=Audio,DEV=0")
	stableRing := id(":CARD=Device,DEV=0")
	if got := CanonicalALSA(id(":2,0"), sound); got != stableVoice {
		t.Fatalf("canonical: %q", got)
	}
	if got := USBRoot(stableVoice, sound); got != voice {
		t.Fatalf("voice: %q", got)
	}
	if err := os.RemoveAll(filepath.Join(sound, "card1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(sound, "card2"), filepath.Join(sound, "card1")); err != nil {
		t.Fatal(err)
	}
	if got := CanonicalALSA(id(":1,0"), sound); got != stableVoice {
		t.Fatalf("renumbered: %q", got)
	}
	if got := USBRoot(stableVoice, sound); got != voice {
		t.Fatalf("renumbered voice: %q", got)
	}
	if got := USBRoot(stableRing, sound); got != "" {
		t.Fatalf("missing ringer selected voice: %q", got)
	}
	if got := USBRoot(id(":1,0"), sound); got != voice {
		t.Fatalf("legacy numeric: %q", got)
	}
	for _, bad := range []string{"not-hex", id(":CARD=../../etc,DEV=0"), id(":CARD=Audio,DEV=-1"), id(":CARD=Audio,DEV=oops")} {
		if got := USBRoot(bad, sound); got != "" {
			t.Errorf("accepted invalid id %q: %q", bad, got)
		}
	}
	for _, value := range []string{"not-hex", id("default"), id(":0,0")} {
		if got := CanonicalALSA(value, sound); got != value {
			t.Errorf("changed non-USB id %q", value)
		}
	}
	add("3", "Audio", "3a07")
	if got := USBRoot(stableVoice, sound); got != "" {
		t.Fatalf("ambiguous card name matched: %q", got)
	}
}
