// Package room implements authenticated, direct UDP audio rooms.
package room

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"time"
)

const (
	SampleRate   = 16000
	FrameSamples = 320
	FrameBytes   = FrameSamples * 2
	MaxPeers     = 32
	maxPacket    = 1400
	hello        = byte(1)
	voice        = byte(2)
)

type packet struct {
	Kind   byte
	Sender [16]byte
	Seq    uint64
	Body   []byte
}
type codec struct {
	lastPrune int64
	aead      cipher.AEAD
	seen      map[[12]byte]int64
}

func newCodec(key []byte) (*codec, error) {
	if len(key) != 32 {
		return nil, errors.New("room key must contain 32 random bytes")
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &codec{aead: a, seen: make(map[[12]byte]int64)}, nil
}
func (c *codec) seal(p packet, now time.Time) ([]byte, error) {
	if len(p.Body) > maxPacket-65 {
		return nil, errors.New("packet too large")
	}
	out := make([]byte, 16)
	copy(out, "WT01")
	if _, err := rand.Read(out[4:]); err != nil {
		return nil, err
	}
	plain := make([]byte, 33+len(p.Body))
	binary.BigEndian.PutUint64(plain, uint64(now.Unix()))
	plain[8] = p.Kind
	copy(plain[9:25], p.Sender[:])
	binary.BigEndian.PutUint64(plain[25:33], p.Seq)
	copy(plain[33:], p.Body)
	return c.aead.Seal(out, out[4:16], plain, out[:4]), nil
}

// open is called by the single network reader. Remember all accepted nonces
// for the entire clock-skew window, including sessions that have disconnected.
func (c *codec) open(data []byte, now time.Time) (packet, error) {
	var p packet
	if len(data) < 65 || len(data) > maxPacket || string(data[:4]) != "WT01" {
		return p, errors.New("invalid packet")
	}
	var nonce [12]byte
	copy(nonce[:], data[4:16])
	if _, ok := c.seen[nonce]; ok {
		return p, errors.New("replayed packet")
	}
	plain, err := c.aead.Open(nil, nonce[:], data[16:], data[:4])
	if err != nil {
		return p, err
	}
	stamp := int64(binary.BigEndian.Uint64(plain))
	t := now.Unix()
	if stamp < t-15 || stamp > t+15 {
		return p, errors.New("expired packet or clock skew exceeds 15 seconds")
	}
	if len(c.seen) >= 100000 {
		return p, errors.New("replay window full")
	}
	p.Kind = plain[8]
	copy(p.Sender[:], plain[9:25])
	p.Seq = binary.BigEndian.Uint64(plain[25:33])
	p.Body = plain[33:]
	if p.Kind != hello && p.Kind != voice && p.Kind != directSignal {
		return packet{}, errors.New("unknown packet kind")
	}
	if p.Kind == voice && len(p.Body) != FrameBytes && len(p.Body) != FrameBytes+16 {
		return packet{}, errors.New("invalid audio frame")
	}
	if p.Kind == directSignal && (len(p.Body) != 34 || p.Body[0] != 1 || p.Body[1] > 3) {
		return packet{}, errors.New("invalid direct heartbeat")
	}
	c.seen[nonce] = stamp + 16
	return p, nil
}
func (c *codec) prune(now time.Time) {
	if now.Unix() == c.lastPrune {
		return
	}
	c.lastPrune = now.Unix()
	for n, expiry := range c.seen {
		if expiry < now.Unix() {
			delete(c.seen, n)
		}
	}
}
