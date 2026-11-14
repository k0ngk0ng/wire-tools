package room

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math"
	"time"
)

const directSignal = byte(3)

type CallStatus struct {
	State string `json:"state"`
	ID    string `json:"id,omitempty"`
	Peer  string `json:"peer,omitempty"`
}

type directState struct {
	state   byte // idle, dialing, ringing, active
	token   [16]byte
	remote  [16]byte
	lastSeq uint64
	since   time.Time
	ended   map[[16]byte]time.Time
	tone    uint64
}

func (r *Room) callStatusLocked() *CallStatus {
	if r.direct == nil {
		return nil
	}
	d := r.direct
	s := &CallStatus{State: []string{"idle", "dialing", "ringing", "active"}[d.state]}
	if d.state != 0 {
		s.ID = hex.EncodeToString(d.token[:])
		s.Peer = hex.EncodeToString(d.remote[:])
	}
	return s
}

func (r *Room) endCallLocked(now time.Time) {
	d := r.direct
	if d.token != [16]byte{} {
		d.ended[d.token] = now.Add(time.Minute)
	}
	d.state, d.tone = 0, 0
	r.selfLevels = levelWindow{}
	r.mixer.Clear()
	for id, p := range r.peers {
		p.levels = levelWindow{}
		r.peers[id] = p
	}
}

func (r *Room) expireCallLocked(now time.Time) {
	if r.direct == nil {
		return
	}
	d := r.direct
	for id, until := range d.ended {
		if now.After(until) {
			delete(d.ended, id)
		}
	}
	if d.remote != [16]byte{} {
		p, ok := r.peers[d.remote]
		if !ok || now.Sub(p.LastSeen) >= 6*time.Second {
			r.endCallLocked(now)
			delete(r.peers, d.remote)
			d.remote, d.lastSeq = [16]byte{}, 0
		}
	}
	if (d.state == 1 || d.state == 2) && now.Sub(d.since) >= 45*time.Second {
		r.endCallLocked(now)
	}
}

// CallAction serializes physical keys and CLI actions against network changes.
func (r *Room) CallAction(action string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.direct == nil {
		return fmt.Errorf("this connection uses room mode; stop talk and select direct mode first")
	}
	now := time.Now()
	r.expireCallLocked(now)
	d := r.direct
	if action == "button" {
		switch d.state {
		case 0:
			action = "dial"
		case 2:
			action = "answer"
		default:
			action = "hangup"
		}
	}
	switch action {
	case "dial":
		if d.state != 0 {
			return fmt.Errorf("already dialing, ringing or in a call")
		}
		if d.remote == [16]byte{} {
			return fmt.Errorf("the other endpoint is offline or is not using direct mode")
		}
		if _, err := rand.Read(d.token[:]); err != nil {
			return err
		}
		d.state, d.since, d.tone = 1, now, 0
	case "answer":
		if d.state != 2 {
			return fmt.Errorf("no incoming call to answer")
		}
		d.state, d.tone = 3, 0
	case "hangup":
		if d.state == 0 {
			return fmt.Errorf("no call to hang up")
		}
		r.endCallLocked(now)
	default:
		return fmt.Errorf("unknown call action")
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
	return nil
}

// Repeated encrypted state advertisements tolerate UDP loss. Packet sequence
// numbers reject reordered state; per-call tokens isolate delayed voice frames.
func (r *Room) directBody() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireCallLocked(time.Now())
	d := r.direct
	b := make([]byte, 34)
	b[0], b[1] = 1, d.state
	copy(b[2:18], d.remote[:])
	copy(b[18:], d.token[:])
	return b
}

func (r *Room) receiveDirectLocked(p packet, now time.Time) bool {
	d := r.direct
	if len(p.Body) != 34 || p.Body[0] != 1 || p.Body[1] > 3 {
		return false
	}
	var target, token [16]byte
	copy(target[:], p.Body[2:18])
	copy(token[:], p.Body[18:])
	state := p.Body[1]
	if target != [16]byte{} && target != r.id || state != 0 && (target != r.id || token == [16]byte{}) {
		return false
	}
	if d.remote != [16]byte{} && d.remote != p.Sender || p.Seq <= d.lastSeq {
		return false
	}
	d.remote, d.lastSeq = p.Sender, p.Seq
	if state == 0 {
		if token != [16]byte{} {
			d.ended[token] = now.Add(time.Minute)
			if d.token == token {
				r.endCallLocked(now)
			}
		}
		return true
	}
	if _, ended := d.ended[token]; ended {
		return true
	}
	if state == 1 {
		if d.state == 0 || d.state == 1 && bytes.Compare(token[:], d.token[:]) < 0 {
			if d.state == 1 {
				d.ended[d.token] = now.Add(time.Minute)
			}
			d.token, d.state, d.since, d.tone = token, 2, now, 0
		}
	} else if state == 3 && d.token == token && d.state == 1 {
		d.state, d.tone = 3, 0
	}
	return true
}

func (r *Room) directAudioLocked(p packet) ([]byte, bool) {
	d := r.direct
	if d == nil {
		return p.Body, len(p.Body) == FrameBytes
	}
	if d.state != 3 || p.Sender != d.remote || len(p.Body) != FrameBytes+16 || !bytes.Equal(p.Body[:16], d.token[:]) {
		return nil, false
	}
	return p.Body[16:], true
}

// CallTone is local playback only: it never enters the network or recording.
func (r *Room) CallTone(out []float64) {
	r.CallToneRouted(out, nil)
}

// CallToneRouted sends incoming alerts to the separate ringer when configured.
// Outgoing ringback remains in the handset, including in split-output mode.
func (r *Room) CallToneRouted(out, ring []float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.direct
	if d == nil || d.state != 1 && d.state != 2 {
		return
	}
	if d.state == 2 && ring != nil {
		out = ring
	}
	for i := range out {
		t := float64(d.tone) / SampleRate
		if d.state == 2 {
			// An incoming telephone ring is distinct from the caller's low
			// ringback: two short bursts of a bright, alternating tone.
			phase := math.Mod(t, 3)
			if phase < .4 || phase >= .6 && phase < 1 {
				frequency := 1100.0
				if int(d.tone/(SampleRate/20))%2 != 0 {
					frequency = 1500
				}
				part := float64(d.tone%(SampleRate/20)) / SampleRate
				envelope := math.Min(1, math.Min(part/.005, (.05-part)/.005))
				angle := 2 * math.Pi * frequency * t
				out[i] += 3500 * envelope * (.75*math.Sin(angle) + .25*math.Sin(2*angle))
			}
		} else if math.Mod(t, 3) < 1 {
			out[i] += 1000 * (math.Sin(2*math.Pi*440*t) + math.Sin(2*math.Pi*480*t))
		}
		d.tone++
	}
}
