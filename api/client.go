// Package api is the local API client shared by wirectl and desktop frontends.
// Ensure starts the installed runtime on demand. Resource requests are never
// retried after transmission, so a connection error cannot duplicate a mutation.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/internal/state"
)

type Endpoint struct {
	Address string `json:"address"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
}

type Client struct {
	Home string
	// Executable is the installed wirectl path. A GUI can set it explicitly.
	Executable string
}

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("local API: %s (HTTP %d)", e.Message, e.Status) }

func DefaultHome() (string, error) {
	if dir := os.Getenv("WIRECTL_HOME"); dir != "" {
		return filepath.Abs(dir)
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Abs(filepath.Join(base, "wirectl"))
}
func New(dir string) *Client {
	if dir == "" {
		dir, _ = DefaultHome()
	}
	return &Client{Home: dir}
}
func (c *Client) Endpoint() (Endpoint, error) {
	var ep Endpoint
	p := filepath.Join(c.Home, "api.json")
	st, err := os.Lstat(p)
	if err != nil {
		return ep, err
	}
	if !st.Mode().IsRegular() || st.Size() > 4096 {
		return ep, errors.New("invalid local API endpoint file")
	}
	if err = checkEndpointMode(st); err != nil {
		return ep, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ep, err
	}
	if err = json.Unmarshal(b, &ep); err != nil {
		return ep, err
	}
	host, port, err := net.SplitHostPort(ep.Address)
	n, e := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || e != nil || n < 1 || n > 65535 || len(ep.Token) != 64 || strings.ContainsAny(ep.Token, "\r\n") {
		return ep, errors.New("invalid local API endpoint; expected authenticated IPv4 loopback")
	}
	return ep, nil
}
func (c *Client) Ensure(ctx context.Context) (Endpoint, error) {
	probe := func() (Endpoint, error) {
		ep, err := c.Endpoint()
		if err != nil {
			return ep, err
		}
		probeCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		_, err = c.do(probeCtx, ep, "GET", "/v1/status", nil)
		return ep, err
	}
	if ep, err := probe(); err == nil {
		return ep, nil
	}
	if err := state.Dir(c.Home); err != nil {
		return Endpoint{}, err
	}
	// Serialize startup attempts across clients; the runtime holds a separate
	// OS lock for its full lifetime, including resource shutdown.
	deadline := time.Now().Add(20 * time.Second)
	var unlock func()
	for {
		var err error
		unlock, err = state.Lock(filepath.Join(c.Home, "startup"))
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return Endpoint{}, ctx.Err()
		}
		if time.Now().After(deadline) {
			return Endpoint{}, fmt.Errorf("waiting for local API startup: %w", err)
		}
		select {
		case <-ctx.Done():
			return Endpoint{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer unlock()
	if ep, err := probe(); err == nil {
		return ep, nil
	}
	executable := c.Executable
	if executable == "" {
		executable = os.Getenv("WIRECTL_EXECUTABLE")
	}
	if executable == "" {
		self, err := os.Executable()
		if err != nil {
			return Endpoint{}, err
		}
		if strings.HasPrefix(filepath.Base(self), "wirectl") {
			executable = self
		} else {
			executable, err = exec.LookPath("wirectl")
			if err != nil {
				return Endpoint{}, fmt.Errorf("locate wirectl runtime: %w", err)
			}
		}
	}
	logPath := filepath.Join(c.Home, "runtime.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 8<<20 {
		_ = os.Rename(logPath, logPath+".1")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return Endpoint{}, err
	}
	cmd := exec.Command(executable, "__api", "--state-dir", c.Home)
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), "WIRECTL_HOME="+c.Home, "WIRECTL_API_CLIENT=0")
	detach(cmd)
	err = cmd.Start()
	log.Close()
	if err != nil {
		return Endpoint{}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		if ep, err := probe(); err == nil {
			return ep, nil
		}
		select {
		case <-ctx.Done():
			return Endpoint{}, ctx.Err()
		case err := <-done:
			return Endpoint{}, fmt.Errorf("local API could not start (%v); see %s", err, logPath)
		case <-time.After(50 * time.Millisecond):
			if time.Now().After(deadline) {
				return Endpoint{}, fmt.Errorf("local API startup timed out; see %s", logPath)
			}
		}
	}
}
func (c *Client) Request(ctx context.Context, method, path string, body, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	data, err := c.Do(ctx, method, path, b)
	if err != nil {
		return err
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (c *Client) RequestExisting(ctx context.Context, method, path string, body, out any) error {
	ep, err := c.Endpoint()
	if err != nil {
		return err
	}
	var b []byte
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	data, err := c.do(ctx, ep, method, path, b)
	if err != nil {
		return err
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (c *Client) Do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	ep, err := c.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, ep, method, path, body)
}
func (c *Client) Open(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	ep, err := c.Ensure(ctx)
	if err != nil {
		return nil, err
	}
	return c.open(ctx, ep, method, path, body)
}
func (c *Client) open(ctx context.Context, ep Endpoint, method, path string, body []byte) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, errors.New("API path must be absolute")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+ep.Address+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	req.Header.Set("Content-Type", "application/json")
	h := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return h.Do(req)
}
func boundedRequestContext(ctx context.Context, method, path string) (context.Context, context.CancelFunc) {
	budget := 5 * time.Minute
	if method == http.MethodPost && path == "/v1/update" {
		budget = 30 * time.Minute
	}
	return context.WithTimeout(ctx, budget)
}
func (c *Client) do(ctx context.Context, ep Endpoint, method, path string, body []byte) ([]byte, error) {
	ctx, cancel := boundedRequestContext(ctx, method, path)
	defer cancel()
	resp, err := c.open(ctx, ep, method, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 32<<20 {
		return nil, errors.New("local API response too large")
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if e.Error == "" {
			e.Error = strings.TrimSpace(string(b))
		}
		return nil, &Error{resp.StatusCode, e.Error}
	}
	return b, nil
}
