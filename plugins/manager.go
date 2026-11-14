// Package plugins owns optional hardware controllers, independent of Talk.
package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/k0ngk0ng/wire-tools/internal/state"
)

type Display struct {
	Text     string `json:"text"`
	Visible  bool   `json:"visible"`
	Blinking bool   `json:"blinking"`
}

type Status struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Supported   bool            `json:"supported"`
	Enabled     bool            `json:"enabled"`
	Running     bool            `json:"running"`
	Online      bool            `json:"online"`
	Error       string          `json:"error,omitempty"`
	Device      string          `json:"device,omitempty"`
	SDKVersion  string          `json:"sdk_version,omitempty"`
	CallState   string          `json:"call_state"`
	LastAction  string          `json:"last_action,omitempty"`
	Actions     uint64          `json:"actions"`
	Display     *Display        `json:"display,omitempty"`
	Buttons     map[string]bool `json:"buttons,omitempty"`
}

var names = []string{"sakshat", "spacetouch"}

func defaultStatus(name string) Status {
	s := Status{Name: name, Supported: supported(name), CallState: "idle"}
	if name == "sakshat" {
		s.Description = "Raspberry Pi SAKS buttons and 6702 call display"
		s.Display = &Display{Text: "6702"}
		s.Buttons = map[string]bool{"s1": false, "s2": false}
	} else {
		s.Description = "USB handset play/pause and volume controls"
	}
	return s
}

func supported(name string) bool {
	if testHardwareSupported(name) {
		return true
	}
	if name == "sakshat" {
		return runtime.GOOS == "linux"
	}
	return runtime.GOOS == "darwin" || runtime.GOOS == "linux"
}

type configuration struct {
	Enabled bool `json:"enabled"`
}

type Manager struct {
	mu         sync.Mutex
	ctx        context.Context
	home       string
	config     map[string]configuration
	active     map[string]*controller
	loadErrors map[string]string
	closed     bool
}

func New(ctx context.Context, home string) *Manager {
	m := &Manager{ctx: ctx, home: home, config: map[string]configuration{}, active: map[string]*controller{}, loadErrors: map[string]string{}}
	for _, name := range names {
		var c configuration
		b, err := os.ReadFile(m.configPath(name))
		if err == nil {
			err = json.Unmarshal(b, &c)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			m.loadErrors[name] = err.Error()
		}
		m.config[name] = c
	}
	return m
}

func (m *Manager) configPath(name string) string {
	return filepath.Join(m.home, "plugins", name, "config.json")
}

func (m *Manager) StartConfigured() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	for _, name := range names {
		if m.config[name].Enabled && m.active[name] == nil && supported(name) {
			m.active[name] = newController(m.ctx, m.home, name)
		}
	}
}

func (m *Manager) snapshot(name string) Status {
	s := defaultStatus(name)
	s.Enabled = m.config[name].Enabled
	if c := m.active[name]; c != nil {
		s = c.snapshot()
		s.Enabled = m.config[name].Enabled
	}
	if err := m.loadErrors[name]; err != "" {
		s.Error = err
	}
	if s.Enabled && !s.Supported {
		s.Error = "plugin is not supported on this platform"
	}
	return s
}

func (m *Manager) SetEnabled(name string, enabled bool) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.config[name]; !ok {
		return Status{}, fmt.Errorf("unknown plugin %q", name)
	}
	if m.closed {
		return Status{}, errors.New("plugin manager is closed")
	}
	if enabled && !supported(name) {
		return m.snapshot(name), errors.New("plugin is not supported on this platform")
	}
	path := m.configPath(name)
	if err := state.Dir(filepath.Dir(path)); err != nil {
		return m.snapshot(name), err
	}
	data, _ := json.Marshal(configuration{Enabled: enabled})
	f, err := os.CreateTemp(filepath.Dir(path), ".plugin-*")
	if err != nil {
		return m.snapshot(name), err
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(temp, path)
	}
	if err != nil {
		return m.snapshot(name), err
	}
	m.config[name] = configuration{Enabled: enabled}
	delete(m.loadErrors, name)
	if enabled {
		if m.active[name] == nil {
			m.active[name] = newController(m.ctx, m.home, name)
		}
	} else if c := m.active[name]; c != nil {
		c.close()
		delete(m.active, name)
	}
	return m.snapshot(name), nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for name, c := range m.active {
		c.close()
		delete(m.active, name)
	}
	return nil
}

func jsonResponse(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" {
		if r.Method != "GET" {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(405)
			return
		}
		m.mu.Lock()
		rows := []Status{}
		for _, name := range names {
			rows = append(rows, m.snapshot(name))
		}
		m.mu.Unlock()
		jsonResponse(w, 200, rows)
		return
	}
	m.mu.Lock()
	_, known := m.config[name]
	m.mu.Unlock()
	if !known {
		jsonResponse(w, 404, map[string]string{"error": "unknown plugin"})
		return
	}
	if r.Method == "GET" {
		m.mu.Lock()
		s := m.snapshot(name)
		m.mu.Unlock()
		jsonResponse(w, 200, s)
		return
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(405)
		return
	}
	var change struct {
		Enabled *bool `json:"enabled"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if err := d.Decode(&change); err != nil {
		jsonResponse(w, 400, map[string]string{"error": err.Error()})
		return
	}
	var extra any
	if change.Enabled == nil || d.Decode(&extra) != io.EOF {
		jsonResponse(w, 400, map[string]string{"error": "provide one enabled boolean"})
		return
	}
	s, err := m.SetEnabled(name, *change.Enabled)
	if err != nil {
		jsonResponse(w, 422, map[string]string{"error": err.Error()})
		return
	}
	jsonResponse(w, 200, s)
}
