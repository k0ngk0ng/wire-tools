package handset

import "github.com/k0ngk0ng/wire-tools/internal/deviceid"

// selectedUSBRoot pins media keys to the configured ALSA capture device, or
// playback device for a file-only/default microphone. An absent explicitly
// selected device must not cause another handset's keys to be claimed.
func selectedUSBRoot(ids []string, soundRoot string) (string, bool) {
	for _, id := range ids {
		if id == "" || id == "none" {
			continue
		}
		return deviceid.USBRoot(id, soundRoot), true
	}
	return "", false
}

func usbDeviceRoot(path string) string {
	return deviceid.USBParent(path)
}
