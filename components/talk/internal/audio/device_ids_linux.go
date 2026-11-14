//go:build linux

package audio

import "github.com/k0ngk0ng/wire-tools/internal/deviceid"

func publicDeviceID(native string) string {
	return deviceid.CanonicalALSA(native, "/sys/class/sound")
}
