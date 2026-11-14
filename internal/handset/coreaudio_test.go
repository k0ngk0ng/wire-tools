package handset

import (
	"encoding/hex"
	"testing"
)

func TestCoreAudioHandsetSelection(t *testing.T) {
	id := func(s string) string { return hex.EncodeToString([]byte(s)) }
	for _, tc := range []struct {
		name            string
		ids             []string
		vendor, product int
		serial          string
		pinned          bool
	}{
		{"spacetouch", []string{id("AppleUSBAudioEngine:SPACETOUCH:USB Audio:000000000:1")}, 0x12d1, 0x3a07, "000000000", true},
		{"ab17x", []string{id("AppleUSBAudioEngine:Generic:AB17X USB Audio:20241228172028:2,1")}, 0x001f, 0x0b23, "20241228172028", true},
		{"pnp", []string{id("AppleUSBAudioEngine:Solid State System Co.,Ltd.:USB PnP Audio Device:0000:1")}, 0x0c76, 0x1203, "0000", true},
		{"playback without microphone", []string{"none", id("AppleUSBAudioEngine:SPACETOUCH:USB Audio:123:2")}, 0x12d1, 0x3a07, "123", true},
		{"unknown usb audio", []string{id("AppleUSBAudioEngine:Other:USB Audio:123:2")}, 0, 0, "", true},
		{"non usb input cannot claim other device", []string{id("iPhone-id"), id("AppleUSBAudioEngine:SPACETOUCH:USB Audio:123:2")}, 0, 0, "", true},
		{"invalid hex", []string{"not-hex"}, 0, 0, "", true},
		{"truncated uid", []string{id("AppleUSBAudioEngine:SPACETOUCH")}, 0, 0, "", true},
		{"default", []string{"", ""}, 0, 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, p, serial, pinned := coreAudioHandset(tc.ids)
			if v != tc.vendor || p != tc.product || serial != tc.serial || pinned != tc.pinned {
				t.Fatalf("got %x:%x %q %v, want %x:%x %q %v", v, p, serial, pinned, tc.vendor, tc.product, tc.serial, tc.pinned)
			}
		})
	}
}
