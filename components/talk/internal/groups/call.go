package groups

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
)

func (m *Manager) CallAction(ref, action string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("session stopped")
	}
	g, err := m.cfg.FindGroup(ref)
	if err != nil {
		return err
	}
	if g.ID() != m.cfg.CurrentID() {
		return fmt.Errorf("select the connection with group use before controlling its call")
	}
	return m.rooms[g.ID()].r.CallAction(action)
}

func (m *Manager) Handset(action string) error {
	if action == "button" {
		return m.CallAction("", action)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return fmt.Errorf("session stopped")
	}
	delta := 2.0
	if action == "volume-down" {
		delta = -2
	} else if action != "volume-up" {
		return fmt.Errorf("unknown handset action")
	}
	db := max(-60, min(24, m.cfg.OutputGainDB+delta))
	next, err := m.cfg.Changed(config.GroupChange{Action: "output-gain", GainDB: &db})
	if err != nil {
		return err
	}
	if err := config.Rewrite(m.dir, next); err != nil {
		return err
	}
	m.cfg = next
	return nil
}

func (m *Manager) serveCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var cmd struct {
		Action string `json:"action"`
		Group  string `json:"group,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
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
	if err := m.CallAction(cmd.Group, cmd.Action); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m.Snapshot())
}
