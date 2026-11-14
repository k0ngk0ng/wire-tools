package handset

import "testing"

// Failure scenarios precede implementation: regressions in existing devices,
// missing SPACETOUCH support, and claiming unrelated VID/PID or keyboard devices.
func TestUSBDeviceNames(t *testing.T) {
	for _, tc := range []struct{ vendor, product, name string }{
		{"001f", "0b23", "AB17X USB Audio"},
		{"0c76", "1203", "USB PnP Audio Device"},
		{"12d1", "3a07", "SPACETOUCH USB Audio"},
		{"12d1", "1203", ""},
		{"0c76", "3a07", ""},
		{"0000", "0000", ""},
		{"", "", ""},
	} {
		if got := usbDeviceName(tc.vendor, tc.product); got != tc.name {
			t.Errorf("%s:%s: got %q, want %q", tc.vendor, tc.product, got, tc.name)
		}
	}
}
