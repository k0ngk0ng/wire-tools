package room

import (
	"encoding/binary"
	"testing"
)

func TestMemberGainDoesNotAffectRecording(t *testing.T) {
	m := NewMixer()
	a, b := [16]byte{1}, [16]byte{2}
	for seq := uint64(1); seq <= 2; seq++ {
		m.Push(a, seq, constantPCM(1000))
		m.Push(b, seq, constantPCM(2000))
	}
	raw, selected := make([]byte, FrameBytes), make([]byte, FrameBytes)
	monitor := make([]float64, FrameSamples)
	m.ReadSelectedMonitor(raw, selected, a, monitor, map[[16]byte]float64{a: 2})
	if monitor[0] != 4000 {
		t.Fatalf("boost affected wrong member: %v", monitor[0])
	}
	if int16(binary.LittleEndian.Uint16(raw)) != 3000 || int16(binary.LittleEndian.Uint16(selected)) != 1000 {
		t.Fatal("playback gain changed recording")
	}
}
