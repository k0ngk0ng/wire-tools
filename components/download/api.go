// Package download exposes downloads as local HTTP resources, independently
// of the terminal frontend and without a manually started download daemon.
package download

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/daemon"
)

type Manager struct {
	mu           sync.Mutex
	ctx          context.Context
	dir, version string
	runtime      *daemon.Managed
	closed       bool
}

func New(ctx context.Context, dir, version string) *Manager {
	return &Manager{ctx: ctx, dir: dir, version: version}
}
func (m *Manager) ensure() (*daemon.Managed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, errors.New("download runtime is closed")
	}
	if m.runtime != nil {
		return m.runtime, nil
	}
	c, err := config.Load(m.dir)
	if errors.Is(err, os.ErrNotExist) {
		_, err = config.Init(m.dir, filepath.Join(m.dir, "downloads"))
		if err == nil {
			c, err = config.Load(m.dir)
		}
	}
	if err != nil {
		return nil, err
	}
	m.runtime, err = daemon.OpenManaged(m.ctx, m.dir, c)
	return m.runtime, err
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	runtime, err := m.ensure()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if r.URL.Path == "/status" && r.Method == "GET" {
		jobs, health := runtime.Store.Snapshot()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"version": m.version, "jobs": jobs, "engines": health})
		return
	}
	if r.URL.Path == "/config" && r.Method == "GET" {
		c, err := config.Load(m.dir)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		c.Secret = ""
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(c)
		return
	}
	if r.URL.Path == "/shutdown" {
		http.Error(w, "download lifecycle is managed by the local API", 410)
		return
	}
	request := r.Clone(r.Context())
	u := *r.URL
	request.URL = &u
	request.URL.Path = "/v1/" + strings.TrimPrefix(r.URL.Path, "/")
	request.Host = "localhost"
	runtime.Handler.ServeHTTP(w, request)
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.runtime != nil {
		return m.runtime.Close()
	}
	return nil
}

// RunEngine dispatches the private native-engine ownership watchdog.
func RunEngine(ctx context.Context, args []string) error { return daemon.RunEngine(ctx, args) }
