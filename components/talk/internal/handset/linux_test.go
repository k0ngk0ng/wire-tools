//go:build linux

package handset

// Isolated decoder failure scenarios, listed before implementation: truncated
// records, unrelated event type/device keys, release, autorepeat without a fresh
// press, invalid values, and each supported function/volume key.
import (
	"encoding/binary"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestLinuxConsumerEvents(t *testing.T) {
	for _, tc := range []struct {
		name          string
		typeCode, key uint16
		value         int32
		usage         uint32
		valid         bool
	}{
		{"play", 1, 164, 1, 205, true},
		{"up", 1, 115, 1, 233, true},
		{"down", 1, 114, 1, 234, true},
		{"release", 1, 164, 0, 205, true},
		{"repeat", 1, 164, 2, 0, false},
		{"other event", 3, 164, 1, 0, false},
		{"other key", 1, 30, 1, 0, false},
		{"negative value", 1, 164, -1, 0, false},
		{"invalid value", 1, 164, 4, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offset := int(unsafe.Sizeof(unix.Timeval{}))
			raw := make([]byte, offset+8)
			binary.NativeEndian.PutUint16(raw[offset:], tc.typeCode)
			binary.NativeEndian.PutUint16(raw[offset+2:], tc.key)
			binary.NativeEndian.PutUint32(raw[offset+4:], uint32(tc.value))
			page, usage, value, ok := decodeLinuxEvent(raw)
			if ok != tc.valid || ok && (page != 12 || usage != tc.usage || value != int(tc.value)) {
				t.Fatalf("decoded %d/%d/%d, valid=%v", page, usage, value, ok)
			}
			for n := 0; n < len(raw); n++ {
				if _, _, _, ok := decodeLinuxEvent(raw[:n]); ok {
					t.Fatalf("accepted %d-byte event", n)
				}
			}
		})
	}
}
