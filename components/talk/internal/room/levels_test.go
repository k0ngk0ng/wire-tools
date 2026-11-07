package room

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func constantPCM(v uint16) []byte {
	p := make([]byte, FrameBytes)
	for i := 0; i < len(p); i += 2 {
		binary.LittleEndian.PutUint16(p[i:], v)
	}
	return p
}
func TestMemberLevelsAreSeparateAuthenticatedAndExpire(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	r, err := Open("127.0.0.1:0", nil, key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	defer func() { cancel(); <-done }()
	c, _ := newCodec(key)
	addr, _ := net.ResolveUDPAddr("udp", r.Status().Listen)
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	send := func(id byte, seq uint64, kind byte, pcm []byte, corrupt bool) {
		b, e := c.seal(packet{Sender: [16]byte{id}, Seq: seq, Kind: kind, Body: pcm}, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		if corrupt {
			b[len(b)-1] ^= 1
		}
		if _, e = conn.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	send(1, 1, voice, constantPCM(8192), false)
	send(2, 1, voice, constantPCM(16384), false)
	until(t, time.Second, func() bool { return r.Status().Received == 2 })
	s := r.Status()
	if len(s.Peers) != 2 || s.Peers[0].Level.RMS != 0.25 || s.Peers[1].Level.RMS != 0.5 {
		t.Fatalf("independent levels: %+v", s.Peers)
	}
	send(1, 2, hello, []byte("[]"), false)
	send(2, 2, voice, constantPCM(32767), true)
	until(t, time.Second, func() bool { return r.Status().Rejected == 1 })
	s = r.Status()
	if s.Peers[0].Level.RMS != 0.25 || s.Peers[1].Level.RMS != 0.5 {
		t.Fatal("hello or rejected packet changed levels")
	}
	until(t, time.Second, func() bool {
		s := r.Status()
		return len(s.Peers) == 2 && s.Peers[0].Level.RMS == 0 && s.Peers[1].Level.RMS == 0
	})
	r.Capture(constantPCM(16384))
	if r.Status().SelfLevel.RMS != 0.5 {
		t.Fatal("missing self level")
	}
}
func TestLevelWindowExpiryAndClipping(t *testing.T) {
	var w levelWindow
	now := time.Unix(100, 0)
	w.add(constantPCM(32768), now)
	l := w.snapshot(now.Add(200 * time.Millisecond))
	if l.RMS != 1 || l.Peak != 1 || !l.Clipped {
		t.Fatalf("%+v", l)
	}
	if w.snapshot(now.Add(400*time.Millisecond)) != (Level{}) {
		t.Fatal("stale peak")
	}
}
