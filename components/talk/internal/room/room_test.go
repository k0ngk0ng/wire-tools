package room

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestProtocolAuthenticationReplayAndTime(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	c, _ := newCodec(key)
	now := time.Now()
	b, err := c.seal(packet{Kind: voice, Body: make([]byte, FrameBytes)}, now)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := append([]byte(nil), b...)
	corrupted[len(corrupted)-1] ^= 1
	if _, err = c.open(corrupted, now); err == nil {
		t.Fatal("accepted tampered packet")
	}
	wrong, _ := newCodec(bytes.Repeat([]byte{8}, 32))
	if _, err = wrong.open(b, now); err == nil {
		t.Fatal("accepted another room")
	}
	if _, err = c.open(b, now); err != nil {
		t.Fatal(err)
	}
	if _, err = c.open(b, now); err == nil {
		t.Fatal("accepted replay")
	}
	c.prune(now.Add(time.Minute))
	if _, err = c.open(b, now.Add(time.Minute)); err == nil {
		t.Fatal("accepted expired packet")
	}
	future, _ := c.seal(packet{Kind: hello}, now.Add(time.Minute))
	if _, err = c.open(future, now); err == nil {
		t.Fatal("accepted future packet")
	}
	for n := 0; n < 65; n++ {
		if _, err = c.open(make([]byte, n), now); err == nil {
			t.Fatal("accepted short packet")
		}
	}
}
func TestMixerOverlapClipReorderAndBounds(t *testing.T) {
	m := NewMixer()
	frame := make([]byte, FrameBytes)
	for i := 0; i < FrameSamples; i++ {
		binary.LittleEndian.PutUint16(frame[2*i:], 20000)
	}
	for _, id := range [][16]byte{{1}, {2}} {
		m.Push(id, 1, frame)
		m.Push(id, 2, frame)
		m.Push(id, 1, frame)
	}
	out := make([]byte, FrameBytes)
	m.Read(out)
	if v := int16(binary.LittleEndian.Uint16(out)); v != 32767 {
		t.Fatalf("mix = %d", v)
	}
	m.Read(out)
	m.Read(out)
	if !bytes.Equal(out, make([]byte, FrameBytes)) {
		t.Fatal("underrun is not silence")
	}
	for n := 3; n < 100; n++ {
		m.Push([16]byte{1}, uint64(n), frame)
	}
	if n := len(m.streams[[16]byte{1}].frames); n != 6 {
		t.Fatalf("queue = %d", n)
	}
}
func TestThreeMembersDiscoverAndExchangeDirectAudio(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := bytes.Repeat([]byte{1}, 32)
	a, err := Open("127.0.0.1:0", nil, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open("127.0.0.1:0", []string{a.Status().Listen}, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Open("127.0.0.1:0", []string{a.Status().Listen}, key)
	if err != nil {
		t.Fatal(err)
	}
	rs := []*Room{a, b, c}
	done := make(chan error, 3)
	for _, r := range rs {
		go func(r *Room) { done <- r.Run(ctx) }(r)
	}
	defer func() {
		cancel()
		for range rs {
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(time.Second):
				t.Error("room failed to stop")
			}
		}
	}()
	until(t, 5*time.Second, func() bool {
		for _, r := range rs {
			if len(r.Status().Peers) != 2 {
				return false
			}
		}
		return true
	})
	frame := make([]byte, FrameBytes)
	binary.LittleEndian.PutUint16(frame, 1234)
	// Two non-bootstrap members must exchange audio even after the bootstrap
	// socket closes: room membership is a mesh, not an audio relay.
	a.Close()
	for i := 0; i < 3; i++ {
		b.Capture(frame)
		time.Sleep(25 * time.Millisecond)
	}
	until(t, time.Second, func() bool { return c.Status().Received >= 2 })
	out := make([]byte, FrameBytes)
	c.Playback(out)
	if !bytes.Equal(out, frame) {
		t.Fatal("speaker did not receive peer samples")
	}
	attacker, err := net.Dial("udp", c.Status().Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	attacker.Write([]byte("garbage"))
	until(t, time.Second, func() bool { return c.Status().Rejected > 0 })
	// Closed a was intentional, consume its error before deferred checks.
	select {
	case <-done:
		done <- nil
	case <-time.After(time.Second):
		t.Fatal("close did not stop room")
	}
}
func until(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition timed out")
}
func FuzzPacketOpen(f *testing.F) {
	f.Add([]byte("WT01"))
	f.Fuzz(func(t *testing.T, b []byte) { c, _ := newCodec(make([]byte, 32)); c.open(b, time.Now()) })
}

func TestSelectedRecordingUsesSameClockAndExcludesOtherPeers(t *testing.T) {
	m := NewMixer()
	for _, pair := range []struct {
		id    [16]byte
		value uint16
	}{{[16]byte{1}, 1000}, {[16]byte{2}, 2000}} {
		frame := make([]byte, FrameBytes)
		for i := 0; i < len(frame); i += 2 {
			binary.LittleEndian.PutUint16(frame[i:], pair.value)
		}
		m.Push(pair.id, 1, frame)
		m.Push(pair.id, 2, frame)
	}
	mixed, selected := make([]byte, FrameBytes), make([]byte, FrameBytes)
	m.ReadSelected(mixed, selected, [16]byte{1})
	for i := 0; i < FrameBytes; i += 2 {
		if binary.LittleEndian.Uint16(mixed[i:]) != 3000 || binary.LittleEndian.Uint16(selected[i:]) != 1000 {
			t.Fatal("selected peer recording leaked other peer or lost alignment")
		}
	}
	m.ReadSelected(mixed, selected, [16]byte{3})
	if !bytes.Equal(selected, make([]byte, FrameBytes)) {
		t.Fatal("missing peer should record silence")
	}
}
