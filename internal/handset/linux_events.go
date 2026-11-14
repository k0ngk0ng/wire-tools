//go:build linux

package handset

import (
	"encoding/binary"
	"unsafe"

	"golang.org/x/sys/unix"
)

func decodeLinuxEvent(raw []byte) (page, usage uint32, value int, ok bool) {
	offset := int(unsafe.Sizeof(unix.Timeval{}))
	if len(raw) != offset+8 || binary.NativeEndian.Uint16(raw[offset:]) != 1 {
		return
	}
	value = int(int32(binary.NativeEndian.Uint32(raw[offset+4:])))
	if value != 0 && value != 1 {
		return
	}
	switch binary.NativeEndian.Uint16(raw[offset+2:]) {
	case 164:
		usage = 205
	case 115:
		usage = 233
	case 114:
		usage = 234
	default:
		return
	}
	return 12, usage, value, true
}
