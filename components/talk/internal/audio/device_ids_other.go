//go:build !linux

package audio

func publicDeviceID(native string) string { return native }
