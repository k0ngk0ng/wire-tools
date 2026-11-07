package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
)

func TestDeviceSelectionUsesProfile(t *testing.T) {
	dir := t.TempDir()
	if id, err := testDeviceID(dir, "input", ""); err != nil || id != "" {
		t.Fatalf("uninitialized profile: %q, %v", id, err)
	}
	c, err := config.New()
	if err != nil {
		t.Fatal(err)
	}
	c.Input, c.Output = "usb-microphone", "usb-speaker"
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, explicit, want string }{
		{"input", "", c.Input},
		{"output", "", c.Output},
		{"input", "override", "override"},
		{"output", "override", "override"},
	} {
		id, err := testDeviceID(dir, tc.kind, tc.explicit)
		if err != nil || id != tc.want {
			t.Fatalf("%+v: %q, %v", tc, id, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := testDeviceID(dir, "input", ""); err == nil {
		t.Fatal("invalid profile silently ignored")
	}
	if id, err := testDeviceID(dir, "input", "override"); err != nil || id != "override" {
		t.Fatalf("explicit device requires profile: %q, %v", id, err)
	}
}
