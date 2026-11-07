// Package connect exposes encrypted connections as local API resources. The
// API runtime owns connection lifetimes; consumers do not install or start a
// connection daemon. Privileged tunnel setup remains an explicit OS action.
package connect

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	connectcli "github.com/k0ngk0ng/wire-tools/components/connect/internal/cli"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/client"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/endpoint"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/localctl"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/netsetup"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/platform"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/secure"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/service"
)

// Run preserves the existing command grammar behind the unified API's command
// compatibility endpoint. The root command owns all user-visible updates.
func Run(ctx context.Context, args []string, version string, in io.Reader, out, errOut io.Writer) error {
	if len(args) > 0 && args[0] == "__connect" {
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "update" {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		command := exec.CommandContext(ctx, executable, append([]string{"update"}, args[1:]...)...)
		command.Stdin, command.Stdout, command.Stderr = in, out, errOut
		return command.Run()
	}
	if os.Getenv("WIRECTL_API_CLIENT") == "1" {
		if handled, err := connectcli.RunAPI(ctx, args, version, in, out, errOut); handled {
			return err
		}
	}
	return connectcli.Run(ctx, args, version, in, out, errOut)
}

// RunService enters the Windows service dispatcher only when SCM owns the
// process. Root calls it around hidden __connect invocations.
func RunService(fn func(context.Context) error) (bool, error) { return service.Run(fn) }

// Status is a snapshot of the active WireGuard transport.
type Status = client.Status

// Connection contains only public connection state, never pairing identities,
// private keys, enrollment credentials, or stored profile secrets.
type Connection struct {
	Name        string `json:"name"`
	Server      string `json:"server,omitempty"`
	PairingCode string `json:"pairing_code,omitempty"`
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
	Status      Status `json:"status"`
}

// CreateRequest starts pairing; an absent host field means host when code is
// empty and guest otherwise. A host response is asynchronous: poll the named
// connection for its pairing_code and eventual connected or failed state.
type CreateRequest struct {
	Name      string `json:"name,omitempty"`
	Server    string `json:"server"`
	Code      string `json:"code,omitempty"`
	Host      *bool  `json:"host,omitempty"`
	Network   string `json:"network,omitempty"`
	Interface string `json:"interface,omitempty"`
	MTU       int    `json:"mtu,omitempty"`
	STUNURL   string `json:"stun_url,omitempty"`
	RelayOnly bool   `json:"relay_only,omitempty"`
	Replace   bool   `json:"replace,omitempty"`
}

type runningConnection struct {
	snapshot Connection
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// Manager is scoped to one private state directory and one API runtime.
type Manager struct {
	ctx         context.Context
	cancel      context.CancelFunc
	store       config.Store
	version     string
	mu          sync.Mutex
	connections map[string]*runningConnection
	closed      bool
	desired     map[string]bool
	restoreDone chan struct{}
	restoreErr  error
	mux         *http.ServeMux
}

func New(ctx context.Context, dir, version string) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	m := &Manager{ctx: ctx, cancel: cancel, store: config.Store{Dir: dir}, version: version, connections: map[string]*runningConnection{}, desired: map[string]bool{}, restoreDone: make(chan struct{}), mux: http.NewServeMux()}
	m.mux.HandleFunc("GET /connections", m.list)
	m.mux.HandleFunc("GET /connections/{name}", m.get)
	m.mux.HandleFunc("POST /connections", m.create)
	m.mux.HandleFunc("DELETE /connections/{name}", m.stop)
	m.mux.HandleFunc("POST /connections/{name}/resume", m.resume)
	m.mux.HandleFunc("POST /credentials", m.enroll)
	m.mux.HandleFunc("GET /doctor", m.doctor)
	go m.restore()
	return m
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) { m.mux.ServeHTTP(w, r) }

func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	<-m.restoreDone
	m.mu.Lock()
	jobs := make([]*runningConnection, 0, len(m.connections))
	for _, job := range m.connections {
		jobs = append(jobs, job)
	}
	m.mu.Unlock()
	for _, job := range jobs {
		<-job.done
	}
	return nil
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func connectionName(name string) (string, error) {
	if name == "" {
		name = "default"
	}
	if !namePattern.MatchString(name) {
		return "", errors.New("name must be 1-32 lowercase letters, digits or hyphens, starting with a letter")
	}
	return name, nil
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, err error) {
	respond(w, status, map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON object")
	}
	return nil
}

func (m *Manager) authorize(server string) (*client.Client, error) {
	parsed, err := endpoint.Parse(server)
	if err != nil {
		return nil, err
	}
	credentials, err := m.store.Credentials()
	if err != nil {
		return nil, err
	}
	credential, ok := credentials[parsed.String()]
	if !ok {
		return nil, fmt.Errorf("server is not authorized; run: wirectl connect login %s", parsed)
	}
	return client.New(parsed.String(), credential)
}

func (m *Manager) snapshots(ctx context.Context) ([]Connection, error) {
	m.mu.Lock()
	restoreErr := m.restoreErr
	m.mu.Unlock()
	if restoreErr != nil {
		return nil, restoreErr
	}

	if err := m.store.Init(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.store.Dir)
	if err != nil {
		return nil, err
	}
	found := map[string]Connection{}
	for _, entry := range entries {
		file := entry.Name()
		if !strings.HasPrefix(file, "profile-") || !strings.HasSuffix(file, ".json") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(file, "profile-"), ".json")
		if !namePattern.MatchString(name) {
			continue
		}
		var profile config.Profile
		if err := m.store.Read("profile-"+name, &profile); err != nil {
			return nil, err
		}
		row := Connection{Name: name, Server: profile.Server, State: "stopped", Status: client.Status{LocalIP: profile.LocalIP, PeerIP: profile.PeerIP, Mode: "stopped"}}
		// Keep sessions created before migration observable without contacting
		// an OS service manager or requiring a background process first.
		probe, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		var status client.Status
		if localctl.Status(probe, m.store.Dir, name, &status) == nil {
			row.Status = status
			row.State = "running"
		}
		cancel()
		found[name] = row
	}
	m.mu.Lock()
	for name, job := range m.connections {
		found[name] = job.snapshot
	}
	m.mu.Unlock()
	rows := make([]Connection, 0, len(found))
	for _, row := range found {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}
func (m *Manager) list(w http.ResponseWriter, r *http.Request) {
	rows, err := m.snapshots(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, 200, map[string]any{"connections": rows})
}
func (m *Manager) get(w http.ResponseWriter, r *http.Request) {
	name, err := connectionName(r.PathValue("name"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	rows, err := m.snapshots(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	for _, row := range rows {
		if row.Name == name {
			respond(w, 200, row)
			return
		}
	}
	fail(w, 404, os.ErrNotExist)
}

func (m *Manager) create(w http.ResponseWriter, r *http.Request) {
	var request *CreateRequest
	if err := decode(w, r, &request); err != nil {
		fail(w, 400, err)
		return
	}
	if request == nil {
		fail(w, 400, errors.New("request must be a JSON object"))
		return
	}
	name, err := connectionName(request.Name)
	if err != nil {
		fail(w, 400, err)
		return
	}
	request.Name = name
	if request.MTU == 0 {
		request.MTU = 1420
	}
	if request.MTU < 576 || request.MTU > 9000 {
		fail(w, 400, errors.New("mtu must be between 576 and 9000"))
		return
	}
	if request.Network == "" {
		request.Network = "100.64.0.0/10"
	}
	network, err := netip.ParsePrefix(request.Network)
	if err != nil || !network.Addr().Is4() || network.Bits() < 8 || network.Bits() > 30 {
		fail(w, 400, errors.New("network must be an IPv4 private/shared prefix between /8 and /30"))
		return
	}
	if request.Code != "" {
		if _, err := secure.ParseCode(request.Code); err != nil {
			fail(w, 400, err)
			return
		}
	}
	m.mu.Lock()
	api, err := m.authorize(request.Server)
	var previous config.Profile
	if err == nil {
		readErr := m.store.Read("profile-"+name, &previous)
		if readErr == nil && !request.Replace {
			err = errors.New("this name is already paired; resume it or choose another name")
		}
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			err = readErr
		}
	}
	m.mu.Unlock()
	if err != nil {
		fail(w, 422, err)
		return
	}
	if err := platform.Check(r.Context()); err != nil {
		fail(w, 422, fmt.Errorf("%w; initialize network access with wirectl connect setup", err))
		return
	}
	<-m.restoreDone
	job, err := m.begin(name, api.Server)
	if err != nil {
		fail(w, 409, err)
		return
	}
	go func() {
		defer close(job.done)
		host := request.Code == ""
		if request.Host != nil {
			host = *request.Host
		}
		profile, err := api.Pair(job.ctx, client.PairOptions{Host: host, Code: request.Code, Network: network, OnCode: func(code string) { m.mu.Lock(); job.snapshot.PairingCode = code; m.mu.Unlock() }})
		if err == nil {
			profile.Interface = interfaceName(request.Interface, name)
			profile.MTU, profile.STUNURL, profile.RelayOnly = request.MTU, request.STUNURL, request.RelayOnly
			m.mu.Lock()
			err = m.store.Write("profile-"+name, profile)
			if err == nil {
				err = m.persistDesiredLocked(name, true)
			}
			m.mu.Unlock()
		}
		if err == nil {
			err = m.run(job, api, profile)
		}
		m.finish(job, err)
	}()
	m.mu.Lock()
	snapshot := job.snapshot
	m.mu.Unlock()
	respond(w, 202, snapshot)
}

func (m *Manager) begin(name, server string) (*runningConnection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return nil, errors.New("connection runtime is shutting down")
	}
	if old, ok := m.connections[name]; ok {
		select {
		case <-old.done:
		default:
			return nil, errors.New("connection is already running")
		}
	}
	ctx, cancel := context.WithCancel(m.ctx)
	job := &runningConnection{snapshot: Connection{Name: name, Server: server, State: "pairing"}, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	m.connections[name] = job
	return job, nil
}
func (m *Manager) finish(job *runningConnection, err error) {
	job.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	job.snapshot.PairingCode = ""
	job.snapshot.State = "stopped"
	job.snapshot.Status.Running = false
	if err != nil && !errors.Is(err, context.Canceled) {
		job.snapshot.Error = err.Error()
		job.snapshot.State = "failed"
	}
}
func (m *Manager) run(job *runningConnection, api *client.Client, profile config.Profile) error {
	ctx := job.ctx
	m.mu.Lock()
	job.snapshot.State = "connecting"
	job.snapshot.PairingCode = ""
	m.mu.Unlock()
	return api.Run(ctx, profile, client.RunOptions{Interface: profile.Interface, MTU: profile.MTU, STUNURL: profile.STUNURL, RelayOnly: profile.RelayOnly, Log: slog.Default(), OnStatus: func(status client.Status) {
		m.mu.Lock()
		job.snapshot.Status = status
		job.snapshot.State = "running"
		m.mu.Unlock()
	}})
}
func interfaceName(requested, name string) string {
	if requested != "" || runtime.GOOS == "darwin" {
		return requested
	}
	identity, _ := netsetup.CurrentIdentity()
	digest := sha256.Sum256([]byte(identity + "\x00" + name))
	return fmt.Sprintf("wc%x", digest[:6])
}
func (m *Manager) resume(w http.ResponseWriter, r *http.Request) {
	<-m.restoreDone
	var request *struct {
		Interface *string `json:"interface,omitempty"`
		MTU       *int    `json:"mtu,omitempty"`
		STUNURL   *string `json:"stun_url,omitempty"`
		RelayOnly *bool   `json:"relay_only,omitempty"`
	}
	if err := decode(w, r, &request); err != nil {
		fail(w, 400, err)
		return
	}
	if request == nil {
		fail(w, 400, errors.New("request must be a JSON object"))
		return
	}

	name, err := connectionName(r.PathValue("name"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	m.mu.Lock()
	var profile config.Profile
	err = m.store.Read("profile-"+name, &profile)
	var api *client.Client
	if err == nil {
		api, err = m.authorize(profile.Server)
	}
	m.mu.Unlock()
	if err != nil {
		status := 422
		if errors.Is(err, os.ErrNotExist) {
			status = 404
		}
		fail(w, status, err)
		return
	}
	if request.Interface != nil {
		profile.Interface = *request.Interface
	}
	if request.MTU != nil {
		if *request.MTU < 576 || *request.MTU > 9000 {
			fail(w, 400, errors.New("mtu must be between 576 and 9000"))
			return
		}
		profile.MTU = *request.MTU
	}
	if request.STUNURL != nil {
		profile.STUNURL = *request.STUNURL
	}
	if request.RelayOnly != nil {
		profile.RelayOnly = *request.RelayOnly
	}
	if err = platform.Check(r.Context()); err != nil {
		fail(w, 422, fmt.Errorf("%w; initialize network access with wirectl connect setup", err))
		return
	}
	m.mu.Lock()
	if old := m.connections[name]; old != nil {
		select {
		case <-old.done:
		default:
			m.mu.Unlock()
			fail(w, 409, errors.New("connection is already running"))
			return
		}
	}
	if request.Interface != nil || request.MTU != nil || request.STUNURL != nil || request.RelayOnly != nil {
		err = m.store.Write("profile-"+name, profile)
	}
	if err == nil {
		err = m.persistDesiredLocked(name, true)
	}
	m.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	job, err := m.begin(name, profile.Server)
	if err != nil {
		fail(w, 409, err)
		return
	}
	go func() { defer close(job.done); m.finish(job, m.run(job, api, profile)) }()
	m.mu.Lock()
	snapshot := job.snapshot
	m.mu.Unlock()
	respond(w, 202, snapshot)
}
func (m *Manager) stop(w http.ResponseWriter, r *http.Request) {
	name, err := connectionName(r.PathValue("name"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	<-m.restoreDone
	m.mu.Lock()
	err = m.persistDesiredLocked(name, false)
	job := m.connections[name]
	m.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}

	if job != nil {
		job.cancel()
		select {
		case <-job.done:
		case <-r.Context().Done():
			fail(w, 408, r.Context().Err())
			return
		}
	} else {
		err := localctl.Stop(r.Context(), m.store.Dir, name)
		if err != nil && !localctl.IsNotRunning(err) && !errors.Is(err, os.ErrNotExist) {
			fail(w, 422, err)
			return
		}
	}
	if job != nil {
		m.mu.Lock()
		job.snapshot.State, job.snapshot.Error, job.snapshot.PairingCode = "stopped", "", ""
		job.snapshot.Status.Running = false
		job.snapshot.Status.Mode = "stopped"
		m.mu.Unlock()
	}
	retained := r.URL.Query().Get("forget") != "true"
	if !retained {
		m.mu.Lock()
		err = m.store.Remove("profile-" + name)
		if err == nil && m.connections[name] == job {
			delete(m.connections, name)
		}
		m.mu.Unlock()
		if err != nil {
			fail(w, 500, err)
			return
		}
	}
	respond(w, 200, map[string]any{"name": name, "state": "stopped", "saved_pair_retained": retained})
}
func (m *Manager) enroll(w http.ResponseWriter, r *http.Request) {
	var request *struct {
		Server            string `json:"server"`
		Token             string `json:"token"`
		Name              string `json:"name,omitempty"`
		CertificateSHA256 string `json:"certificate_sha256,omitempty"`
	}
	if err := decode(w, r, &request); err != nil {
		fail(w, 400, err)
		return
	}
	if request == nil || request.Token == "" {
		fail(w, 400, errors.New("server enrollment token is required"))
		return
	}
	api, err := client.New(request.Server, config.Credential{CertificateSHA256: request.CertificateSHA256})
	if err != nil {
		fail(w, 400, err)
		return
	}
	if request.Name == "" {
		request.Name, _ = os.Hostname()
	}
	credential, err := api.Login(r.Context(), request.Token, request.Name)
	if err != nil {
		fail(w, 422, err)
		return
	}
	m.mu.Lock()
	credentials, err := m.store.Credentials()
	if err == nil {
		credentials[api.Server] = credential
		err = m.store.Write("credentials", credentials)
	}
	m.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	respond(w, 201, map[string]string{"server": api.Server, "device_id": credential.DeviceID})
}
func (m *Manager) doctor(w http.ResponseWriter, r *http.Request) {
	issues := []string{}
	if err := platform.Check(r.Context()); err != nil {
		issues = append(issues, err.Error())
	}
	if server := r.URL.Query().Get("server"); server != "" {
		m.mu.Lock()
		api, err := m.authorize(server)
		m.mu.Unlock()
		if err == nil {
			err = api.Health(r.Context())
		}
		if err != nil {
			issues = append(issues, err.Error())
		}
	}
	respond(w, 200, map[string]any{"platform": runtime.GOOS + "/" + runtime.GOARCH, "version": m.version, "ready": len(issues) == 0, "issues": issues})
}

// StateDir resolves an explicit state directory before the old per-project
// default. WIRECTL_HOME allows CLI and API calls to share the unified layout.
func StateDir() (string, error) {
	if dir := os.Getenv("WIRECTL_CONNECT_HOME"); dir != "" {
		return filepath.Abs(dir)
	}
	if dir := os.Getenv("WIRECTL_HOME"); dir != "" {
		return filepath.Abs(filepath.Join(dir, "connect"))
	}
	return config.DefaultDir()
}

// persistDesiredLocked records intent separately from credentials. Runtime
// shutdown leaves this file intact; an explicit stop removes only the name.
func (m *Manager) persistDesiredLocked(name string, active bool) error {
	if m.restoreErr != nil {
		return m.restoreErr
	}
	next := make(map[string]bool, len(m.desired)+1)
	for saved := range m.desired {
		next[saved] = true
	}
	if active {
		next[name] = true
	} else {
		delete(next, name)
	}
	names := make([]string, 0, len(next))
	for saved := range next {
		names = append(names, saved)
	}
	sort.Strings(names)
	if err := m.store.Write("active-connections", names); err != nil {
		return err
	}
	m.desired = next
	return nil
}

func (m *Manager) restore() {
	defer close(m.restoreDone)
	// A first API status request does not need a connect state directory.
	if _, err := os.Lstat(filepath.Join(m.store.Dir, "active-connections.json")); errors.Is(err, os.ErrNotExist) {
		return
	}
	m.mu.Lock()
	var names []string
	err := m.store.Read("active-connections", &names)
	if err == nil {
		for _, name := range names {
			if !namePattern.MatchString(name) {
				err = errors.New("invalid active connection name")
				break
			}
			m.desired[name] = true
		}
	}
	if err != nil {
		m.restoreErr = fmt.Errorf("restore desired connections: %w", err)
	}
	m.mu.Unlock()
	if err != nil {
		return
	}
	for _, name := range names {
		if m.ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		var profile config.Profile
		err = m.store.Read("profile-"+name, &profile)
		var api *client.Client
		if err == nil {
			api, err = m.authorize(profile.Server)
		}
		m.mu.Unlock()
		if err == nil {
			err = platform.Check(m.ctx)
		}
		if err != nil {
			child, cancel := context.WithCancel(m.ctx)
			cancel()
			done := make(chan struct{})
			close(done)
			m.mu.Lock()
			if !m.closed {
				m.connections[name] = &runningConnection{snapshot: Connection{Name: name, Server: profile.Server, State: "failed", Error: err.Error()}, ctx: child, cancel: cancel, done: done}
			}
			m.mu.Unlock()
			continue
		}
		job, err := m.begin(name, profile.Server)
		if err != nil {
			continue
		}
		go func() { defer close(job.done); m.finish(job, m.run(job, api, profile)) }()
	}
}
