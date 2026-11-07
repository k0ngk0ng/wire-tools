// Package groups shares one audio device pair across independent encrypted rooms.
package groups

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/media"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/volume"
)

type Status struct {
	MicrophoneState string             `json:"microphone_state,omitempty"`
	PeerGains       map[string]float64 `json:"peer_gains,omitempty"`
	RoomID          string             `json:"room_id"`
	Name            string             `json:"name"`
	Current         bool               `json:"current"`
	Listening       bool               `json:"listening"`
	Online          bool               `json:"online"`
	Error           string             `json:"error,omitempty"`
	room.Status
	Media media.Status `json:"media"`
}
type Snapshot struct {
	OutputGainDB float64  `json:"output_gain_db"`
	Current      string   `json:"current"`
	Groups       []Status `json:"groups"`
}
type liveRoom struct {
	r      *room.Room
	m      *media.Session
	cancel context.CancelFunc
	done   chan struct{}
	online atomic.Bool
	err    atomic.Value
}

func openRoom(ctx context.Context, g config.Group) (*liveRoom, error) {
	key, err := (config.Config{Key: g.Key}).KeyBytes()
	if err != nil {
		return nil, err
	}
	r, err := room.OpenMode(g.Listen, g.Peers, key, g.Mode)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	l := &liveRoom{r: r, m: media.New(r), cancel: cancel, done: make(chan struct{})}
	l.online.Store(true)
	go func() {
		defer close(l.done)
		if err := r.Run(ctx); err != nil {
			l.err.Store(err.Error())
		}
		l.online.Store(false)
	}()
	return l, nil
}
func (l *liveRoom) close() { l.cancel(); l.r.Close(); <-l.done; l.m.Close() }

type Manager struct {
	microphoneState func() string
	mu              sync.Mutex
	ctx             context.Context
	dir             string
	cfg             config.Config
	rooms           map[string]*liveRoom
	closed          bool
	limiter         volume.Limiter
}

func Open(ctx context.Context, dir string, c config.Config) (*Manager, error) {
	m := &Manager{ctx: ctx, dir: dir, cfg: c, rooms: map[string]*liveRoom{}}
	for _, g := range c.RoomConfigs() {
		live, err := openRoom(ctx, g)
		if err != nil {
			m.Close()
			return nil, fmt.Errorf("room %s: %w", g.Name, err)
		}
		m.rooms[g.ID()] = live
	}
	return m, nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	for _, l := range m.rooms {
		l.close()
	}
}
func (m *Manager) SetMicrophoneState(f func() string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.microphoneState = f
}

func (m *Manager) snapshot() Snapshot {
	mic := "unknown"
	if m.microphoneState != nil {
		mic = m.microphoneState()
	}
	s := Snapshot{OutputGainDB: m.cfg.OutputGainDB, Current: m.cfg.CurrentID(), Groups: []Status{}}
	for _, g := range m.cfg.RoomConfigs() {
		l := m.rooms[g.ID()]
		status := Status{MicrophoneState: mic, PeerGains: g.PeerGains, RoomID: g.ID(), Name: g.Name, Current: g.ID() == s.Current, Listening: !g.Muted, Online: l.online.Load(), Status: l.r.Status(), Media: l.m.Status()}
		if err := l.err.Load(); err != nil {
			status.Error = err.(string)
		}
		s.Groups = append(s.Groups, status)
	}
	return s
}
func (m *Manager) Snapshot() Snapshot { m.mu.Lock(); defer m.mu.Unlock(); return m.snapshot() }
func (m *Manager) Capture(p []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	if l := m.rooms[m.cfg.CurrentID()]; l.online.Load() {
		l.m.Capture(p)
	}
}
func (m *Manager) Playback(out []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(out)
	if m.closed {
		return
	}
	sums := [room.FrameSamples]float64{}
	monitor := [room.FrameSamples]float64{}
	frame := [room.FrameBytes]byte{}
	for _, g := range m.cfg.RoomConfigs() {
		l := m.rooms[g.ID()]
		l.m.PlaybackMonitor(frame[:], monitor[:], g.PeerGains) // Drain even muted rooms; recordings remain independent.
		l.r.CallTone(monitor[:])
		if g.Muted || !l.online.Load() {
			continue
		}
		for i := range sums {
			sums[i] += monitor[i]
		}
	}
	m.limiter.Render(out, sums[:], volume.Factor(m.cfg.OutputGainDB))
}
func (m *Manager) Apply(cmd config.GroupChange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("session stopped")
	}
	if cmd.Action == "mode" {
		return fmt.Errorf("stop talk before changing connection mode")
	}
	if cmd.Action == "use" {
		if call := m.rooms[m.cfg.CurrentID()].r.Status().Call; call != nil && call.State != "idle" {
			return fmt.Errorf("hang up before switching the speaking connection")
		}
	}
	if cmd.Action == "peer-gain" {
		g, err := m.cfg.FindGroup(cmd.Room)
		if err != nil {
			return err
		}
		if _, err := volume.Address(cmd.Peer); err != nil {
			found := false
			for _, p := range m.rooms[g.ID()].r.Status().Peers {
				if p.ID == cmd.Peer {
					cmd.Peer = p.Address
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("member not found; use an IP:port or full node ID from group status")
			}
		}
	}
	next, err := m.cfg.Changed(cmd)
	if err != nil {
		return err
	}
	var added *liveRoom
	if cmd.Action == "add" {
		added, err = openRoom(m.ctx, *cmd.Group)
		if err != nil {
			return err
		}
		// Persist the selected ephemeral port, so this room can issue invitations.
		next.Groups[len(next.Groups)-1].Listen = added.r.Status().Listen
	}
	if err = config.Rewrite(m.dir, next); err != nil {
		if added != nil {
			added.close()
		}
		return err
	}
	if next.CurrentID() != m.cfg.CurrentID() {
		old := m.rooms[m.cfg.CurrentID()]
		if state := old.m.Status().Input.State; state == "playing" || state == "paused" {
			_ = old.m.InputAction("stop")
		}
	}
	if added != nil {
		m.rooms[cmd.Group.ID()] = added
	}
	if cmd.Action == "leave" {
		g, _ := m.cfg.FindGroup(cmd.Room)
		m.rooms[g.ID()].close()
		delete(m.rooms, g.ID())
	}
	m.cfg = next
	return nil
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/call" {
		m.serveCall(w, r)
		return
	}
	if r.URL.Path == "/media" {
		// Resolve the target under the same lock as routing changes. File input may
		// only start in the speaking room. Recording can be controlled per room.
		m.mu.Lock()
		defer m.mu.Unlock()
		g, err := m.cfg.FindGroup(r.URL.Query().Get("group"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if g.ID() != m.cfg.CurrentID() {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			var cmd media.Command
			if err = json.Unmarshal(body, &cmd); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			if cmd.Action == "input-start" || cmd.Action == "input-resume" {
				http.Error(w, "select this room with group use before sending file audio", 400)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		m.rooms[g.ID()].m.ServeHTTP(w, r)
		return
	}
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(m.Snapshot())
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	var cmd config.GroupChange
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cmd); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		http.Error(w, "expected one command", 400)
		return
	}
	if err := m.Apply(cmd); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.Snapshot())
}
