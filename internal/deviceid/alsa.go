// Package deviceid resolves local audio identities without native driver access.
package deviceid

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func cardNameValid(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func parse(id string) (card, device string, named, valid bool) {
	raw, err := hex.DecodeString(id)
	if err != nil || !strings.HasPrefix(string(raw), ":") {
		return
	}
	card, device, valid = strings.Cut(string(raw)[1:], ",")
	if !valid {
		return
	}
	if strings.HasPrefix(card, "CARD=") {
		named = true
		card = strings.TrimPrefix(card, "CARD=")
		if !cardNameValid(card) || !strings.HasPrefix(device, "DEV=") {
			valid = false
			return
		}
		device = strings.TrimPrefix(device, "DEV=")
	} else {
		n, err := strconv.Atoi(card)
		if err != nil || n < 0 {
			valid = false
			return
		}
		card = strconv.Itoa(n)
	}
	d, err := strconv.Atoi(device)
	if err != nil || d < 0 {
		valid = false
		return
	}
	device = strconv.Itoa(d)
	return
}

// CanonicalALSA replaces an ephemeral USB card number with its ALSA CARD name.
// Native enumeration is still used when opening the actual device.
func CanonicalALSA(id, soundRoot string) string {
	card, device, named, ok := parse(id)
	if !ok || named {
		return id
	}
	directory := filepath.Join(soundRoot, "card"+card)
	root := USBParent(filepath.Join(directory, "device"))
	nameBytes, err := os.ReadFile(filepath.Join(directory, "id"))
	name := strings.TrimSpace(string(nameBytes))
	if root == "" || err != nil || !cardNameValid(name) {
		return id
	}
	stable := hex.EncodeToString([]byte(":CARD=" + name + ",DEV=" + device))
	if USBRoot(stable, soundRoot) != root {
		return id
	}
	return stable
}

// USBRoot accepts both historical numeric IDs and stable ALSA CARD identities.
// Missing/ambiguous named cards fail closed instead of selecting another device.
func USBRoot(id, soundRoot string) string {
	card, _, named, ok := parse(id)
	if !ok {
		return ""
	}
	if !named {
		return USBParent(filepath.Join(soundRoot, "card"+card, "device"))
	}
	paths, _ := filepath.Glob(filepath.Join(soundRoot, "card[0-9]*"))
	result := ""
	for _, path := range paths {
		name, err := os.ReadFile(filepath.Join(path, "id"))
		if err != nil || strings.TrimSpace(string(name)) != card {
			continue
		}
		root := USBParent(filepath.Join(path, "device"))
		if root == "" || result != "" {
			return ""
		}
		result = root
	}
	return result
}

func USBParent(path string) string {
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	for {
		_, v := os.Stat(filepath.Join(path, "idVendor"))
		_, p := os.Stat(filepath.Join(path, "idProduct"))
		if v == nil && p == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}
