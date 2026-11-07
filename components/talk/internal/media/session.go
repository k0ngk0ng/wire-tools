// Package media records received audio and supplies file audio to a live room.
package media

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

type Status struct {
	Recording RecordStatus `json:"recording"`
	Input     InputStatus  `json:"file_input"`
}
type Session struct {
	mu          sync.Mutex
	room        *room.Room
	record      *recording
	player      *player
	selected    [16]byte
	selectedPCM [room.FrameBytes]byte
	closed      bool
}

func New(r *room.Room) *Session { return &Session{room: r} }
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := Status{Recording: RecordStatus{State: "stopped"}, Input: InputStatus{State: "stopped"}}
	if s.record != nil {
		v.Recording = s.record.snapshot()
	}
	if s.player != nil {
		v.Input = s.player.status
	}
	return v
}
func (s *Session) StartRecording(path, peer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}
	if s.record != nil && s.record.snapshot().State == "recording" {
		return fmt.Errorf("already recording; stop it first")
	}
	if s.record != nil {
		_ = s.record.stop()
	}
	s.selected = [16]byte{}
	if peer != "" && peer != "all" {
		found := false
		for _, p := range s.room.Status().Peers {
			if peer == p.ID || peer == p.Address {
				b, _ := hex.DecodeString(p.ID)
				copy(s.selected[:], b)
				peer = p.ID
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("peer is not connected; use its full ID or address from talk status")
		}
	} else {
		peer = "all"
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	r, err := openRecording(path, peer)
	if err != nil {
		return err
	}
	s.record = r
	return nil
}
func (s *Session) StopRecording() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.record == nil {
		return fmt.Errorf("not recording")
	}
	return s.record.stop()
}
func (s *Session) Playback(out []byte) { s.PlaybackMonitor(out, nil, nil) }

func (s *Session) PlaybackMonitor(out []byte, monitor []float64, gains map[string]float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.room.PlaybackSelectedMonitor(out, s.selectedPCM[:], s.selected, monitor, gains)
	if s.record != nil && !s.closed {
		pcm := out
		if s.record.status.Peer != "all" {
			pcm = s.selectedPCM[:]
		}
		s.record.enqueue(pcm)
	}
}
func (s *Session) StartInput(path, mode string, loop bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return fmt.Errorf("session closed")
	}
	if s.player != nil && (s.player.status.State == "playing" || s.player.status.State == "paused") {
		return fmt.Errorf("file input is active; stop it first")
	}
	if s.player != nil {
		s.player.stop()
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	p, err := openPlayer(path, mode, loop)
	if err != nil {
		return err
	}
	s.player = p
	return nil
}
func (s *Session) InputAction(action string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.player
	if p == nil {
		return fmt.Errorf("no file input")
	}
	switch action {
	case "stop":
		p.stop()
		p.status.State = "stopped"
	case "pause":
		if p.status.State != "playing" {
			return fmt.Errorf("file input is not playing")
		}
		p.status.State = "paused"
	case "resume":
		if p.status.State != "paused" {
			return fmt.Errorf("file input is not paused")
		}
		p.status.State = "playing"
	default:
		return fmt.Errorf("unknown input action")
	}
	return nil
}
func (s *Session) Capture(pcm []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	p := s.player
	if p != nil && (p.status.State == "playing" || p.status.State == "paused") {
		if p.status.Mode == "replace" {
			clear(pcm)
		}
		if p.status.State == "playing" {
			select {
			case frame := <-p.frames:
				if frame.end {
					p.status.State = "ended"
					if frame.err != nil {
						p.status.State = "error"
						p.status.Error = frame.err.Error()
					}
				} else {
					for i := 0; i < len(pcm); i += 2 {
						v := int32(int16(binary.LittleEndian.Uint16(pcm[i:]))) + int32(int16(binary.LittleEndian.Uint16(frame.pcm[i:])))
						if v > 32767 {
							v = 32767
						}
						if v < -32768 {
							v = -32768
						}
						binary.LittleEndian.PutUint16(pcm[i:], uint16(int16(v)))
					}
					p.status.Frames++
				}
			default:
				p.status.Underruns++
			}
		}
	}
	s.room.Capture(pcm)
}
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.player != nil {
		s.player.stop()
	}
	if s.record != nil {
		_ = s.record.stop()
	}
}
