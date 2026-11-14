package handset

import (
	"encoding/hex"
	"strings"
)

// Bind HID matching to the model and serial in the configured CoreAudio UID.
func coreAudioHandset(ids []string) (vendor, product int, serial string, pinned bool) {
	for _, id := range ids {
		if id == "" || id == "none" {
			continue
		}
		raw, err := hex.DecodeString(id)
		if err != nil {
			return 0, 0, "", true
		}
		parts := strings.Split(string(raw), ":")
		if len(parts) < 5 || parts[0] != "AppleUSBAudioEngine" {
			return 0, 0, "", true
		}
		switch {
		case parts[2] == "AB17X USB Audio":
			vendor, product = 0x001f, 0x0b23
		case parts[2] == "USB PnP Audio Device":
			vendor, product = 0x0c76, 0x1203
		case parts[1] == "SPACETOUCH" && parts[2] == "USB Audio":
			vendor, product = 0x12d1, 0x3a07
		default:
			return 0, 0, "", true
		}
		return vendor, product, parts[3], true
	}
	return 0, 0, "", false
}
