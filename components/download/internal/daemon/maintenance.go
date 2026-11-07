package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/auth"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
)

// Maintenance serves configuration resources while this runtime owns the state
// lock. Unlike legacy CLI helpers, these operations never reacquire that lock.
func (m *Managed) Maintenance(w http.ResponseWriter, r *http.Request) bool {
	respond := func(code int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(value)
	}
	fail := func(code int, err error) { respond(code, map[string]string{"error": err.Error()}) }
	decode := func(value any) error {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		return d.Decode(value)
	}
	switch r.URL.Path {
	case "/trackers":
		switch r.Method {
		case "GET":
			c, err := config.Load(m.dir)
			if err != nil {
				fail(500, err)
				return true
			}
			if c.Trackers == nil {
				c.Trackers = []string{}
			}
			respond(200, c.Trackers)
		case "PUT", "POST":
			var req struct {
				Operation string   `json:"operation,omitempty"`
				Trackers  []string `json:"trackers"`
			}
			if err := decode(&req); err != nil {
				fail(400, err)
				return true
			}
			if r.Method == "PUT" {
				req.Operation = "replace"
			}
			if req.Trackers == nil || len(req.Trackers) > 256 {
				fail(400, errors.New("trackers must be an array with at most 256 entries"))
				return true
			}
			for _, t := range req.Trackers {
				if err := config.ValidateTracker(t); err != nil {
					fail(400, err)
					return true
				}
			}
			trackers, err := m.UpdateTrackers(r.Context(), req.Operation, req.Trackers)
			if err != nil {
				fail(422, err)
				return true
			}
			respond(200, trackers)
		default:
			w.Header().Set("Allow", "GET, PUT, POST")
			fail(405, errors.New("method not allowed"))
		}
		return true
	case "/servers":
		switch r.Method {
		case "GET":
			backend, err := m.amule.get()
			if err != nil {
				fail(503, err)
				return true
			}
			output, err := backend.(*engine.AMule).ShowServers(r.Context())
			if err != nil {
				fail(502, err)
				return true
			}
			respond(200, map[string]string{"output": string(output)})
		case "POST":
			var req struct {
				URLs []string `json:"urls,omitempty"`
				Data []byte   `json:"data,omitempty"`
			}
			if err := decode(&req); err != nil {
				fail(400, err)
				return true
			}
			if err := m.refreshServers(r.Context(), req.URLs, req.Data); err != nil {
				fail(422, err)
				return true
			}
			respond(200, map[string]bool{"updated": true})
		default:
			w.Header().Set("Allow", "GET, POST")
			fail(405, errors.New("method not allowed"))
		}
		return true
	case "/auth/sessions":
		m.maintenanceMu.Lock()
		defer m.maintenanceMu.Unlock()
		switch r.Method {
		case "POST":
			var session auth.Session
			if err := decode(&session); err != nil {
				fail(400, err)
				return true
			}
			if err := session.Validate(); err != nil {
				fail(400, err)
				return true
			}
			if err := auth.Save(m.dir, session); err != nil {
				fail(422, err)
				return true
			}
			respond(201, map[string]any{"origin": session.Origin, "saved": true})
		case "DELETE":
			origin := r.URL.Query().Get("origin")
			if _, err := auth.Origin(origin); err != nil {
				fail(400, err)
				return true
			}
			if err := auth.Forget(m.dir, origin); err != nil {
				fail(422, err)
				return true
			}
			respond(200, map[string]bool{"removed": true})
		default:
			w.Header().Set("Allow", "POST, DELETE")
			fail(405, errors.New("method not allowed"))
		}
		return true
	}
	return false
}
func (m *Managed) UpdateTrackers(ctx context.Context, operation string, values []string) ([]string, error) {
	m.maintenanceMu.Lock()
	defer m.maintenanceMu.Unlock()
	path := filepath.Join(m.dir, "config.json")
	original, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c config.Config
	if err = json.Unmarshal(original, &c); err != nil {
		return nil, err
	}
	previous := append([]string{}, c.Trackers...)
	next := append([]string{}, previous...)
	switch operation {
	case "replace":
		next = append([]string{}, values...)
	case "add":
		for _, value := range values {
			found := false
			for _, t := range next {
				if t == value {
					found = true
					break
				}
			}
			if !found {
				next = append(next, value)
			}
		}
	case "remove":
		for _, value := range values {
			found := false
			kept := next[:0]
			for _, t := range next {
				if t == value {
					found = true
				} else {
					kept = append(kept, t)
				}
			}
			if !found {
				return nil, fmt.Errorf("tracker %q is not configured", value)
			}
			next = kept
		}
	default:
		return nil, errors.New("operation must be add, remove or replace")
	}
	c.Trackers = next
	if err = c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	ariaPath := filepath.Join(m.dir, "aria2", "aria2.conf")
	ariaOriginal, err := os.ReadFile(ariaPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(ariaOriginal), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "bt-tracker=") {
			lines[i] = "bt-tracker=" + strings.Join(next, ",")
		}
	}
	if err = atomicWrite(path, append(data, '\n')); err != nil {
		return nil, err
	}
	rollback := func() { _ = atomicWrite(path, original); _ = atomicWrite(ariaPath, ariaOriginal) }
	if err = atomicWrite(ariaPath, []byte(strings.Join(lines, "\n"))); err != nil {
		rollback()
		return nil, err
	}
	m.aria.mu.Lock()
	backend := m.aria.backend
	m.aria.mu.Unlock()
	if aria, ok := backend.(*engine.Aria2); ok {
		if err = aria.UpdateTrackers(ctx, next); err != nil {
			rollback()
			_ = aria.UpdateTrackers(ctx, previous)
			return nil, fmt.Errorf("apply trackers to running engine: %w", err)
		}
	}
	return next, nil
}
func (m *Managed) RefreshServers(ctx context.Context, urls []string) error {
	return m.refreshServers(ctx, urls, nil)
}
func (m *Managed) refreshServers(ctx context.Context, urls []string, met []byte) error {
	m.maintenanceMu.Lock()
	defer m.maintenanceMu.Unlock()
	c, err := config.Load(m.dir)
	if err != nil {
		return err
	}
	if urls == nil {
		urls = c.ServerListURLs
	}
	// Validate/download first. Invalid responses never reach the native core.
	if met != nil {
		if err = ValidateServerMet(met); err != nil {
			return err
		}
		if err = atomicWrite(filepath.Join(m.dir, "amule", "server.met"), met); err != nil {
			return err
		}
	} else if err = BootstrapServers(ctx, m.dir, urls, true); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "amule", "server.met"))
	if err != nil {
		return err
	}
	backend, err := m.amule.get()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	fetched := make(chan struct{}, 1)
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/servers.met" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.Write(data)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case fetched <- struct{}{}:
		default:
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	// aMule loads server-list links asynchronously, including while connected.
	// A private loopback URL transfers only the already validated met bytes.
	link := "ed2k://|serverlist|http://" + listener.Addr().String() + "/servers.met|/"
	if _, err = backend.(*engine.AMule).Add(ctx, link); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(15 * time.Second):
		return errors.New("aMule did not fetch the refreshed server list")
	case <-fetched:
		return nil
	}
}
