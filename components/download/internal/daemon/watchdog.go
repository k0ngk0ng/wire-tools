package daemon

import (
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
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/filelock"
)

type engineSpec struct {
	Binary        string   `json:"binary"`
	Args          []string `json:"args"`
	Log           string   `json:"log"`
	OwnershipPipe bool     `json:"ownership_pipe,omitempty"`
}
type engineOwner struct {
	Address string `json:"address"`
	Token   string `json:"token"`
	PID     int    `json:"pid"`
}

// startOwnedProcess delegates native engines to a tiny built-in watchdog.
// The watchdog survives API process crashes, owns an OS lock until its engine
// exits, and observes the original endpoint capability rather than a reusable
// numeric PID. A replacement runtime cannot start a second writer first.
func startOwnedProcess(binary string, args []string, logPath string) (*process, error) {
	home := filepath.Dir(filepath.Dir(filepath.Dir(logPath)))
	endpoint, err := os.ReadFile(filepath.Join(home, "api.json"))
	if errors.Is(err, os.ErrNotExist) {
		return startProcess(binary, args, logPath)
	}
	if err != nil {
		return nil, err
	}
	var owner engineOwner
	if err = json.Unmarshal(endpoint, &owner); err != nil {
		return nil, err
	}
	if err = validateEngineOwner(owner); err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	spec, err := json.Marshal(engineSpec{Binary: binary, Args: args, Log: logPath, OwnershipPipe: true})
	if err != nil {
		return nil, err
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	cmd := exec.Command(self, "__download-engine", string(spec))
	cmd.Stdin = reader
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.Env = append(os.Environ(), "WIRECTL_ENGINE_OWNER="+string(endpoint))
	if err = cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		log.Close()
		return nil, err
	}
	reader.Close()
	p := &process{cmd: cmd, done: make(chan struct{}), log: log, stopTimeout: 20 * time.Second, ownership: writer}
	go func() { p.err = cmd.Wait(); writer.Close(); log.Close(); close(p.done) }()
	return p, nil
}
func validateEngineOwner(owner engineOwner) error {
	host, port, err := net.SplitHostPort(owner.Address)
	if err != nil || host != "127.0.0.1" || port == "" || len(owner.Token) != 64 || strings.ContainsAny(owner.Token, "\r\n") || owner.PID < 1 {
		return errors.New("invalid engine owner endpoint")
	}
	return nil
}

// RunEngine is the private subprocess entry point called by the unified binary.
// It does not expose native process execution through any HTTP resource.
func RunEngine(ctx context.Context, args []string) error {
	if len(args) != 1 || len(args[0]) > 64<<10 {
		return errors.New("invalid engine watchdog invocation")
	}
	var spec engineSpec
	if err := json.Unmarshal([]byte(args[0]), &spec); err != nil {
		return err
	}
	if spec.Binary == "" || !filepath.IsAbs(spec.Log) {
		return errors.New("invalid engine watchdog paths")
	}
	if spec.OwnershipPipe {
		owned, cancel := context.WithCancel(ctx)
		defer cancel()
		ctx = owned
		// Closing the parent's writer requests shutdown; an API crash produces
		// the same EOF automatically on every supported OS, including Windows.
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	}
	var owner engineOwner
	if err := json.Unmarshal([]byte(os.Getenv("WIRECTL_ENGINE_OWNER")), &owner); err != nil {
		return err
	}
	if err := validateEngineOwner(owner); err != nil {
		return err
	}
	home := filepath.Dir(filepath.Dir(filepath.Dir(spec.Log)))
	// This capability is intentionally captured once. Reading a replacement
	// runtime's token would accidentally adopt an orphan into a new owner.
	alive := func() bool {
		b, err := os.ReadFile(filepath.Join(home, "api.json"))
		if err != nil {
			return false
		}
		var current engineOwner
		if json.Unmarshal(b, &current) != nil || current.PID != owner.PID || current.Token != owner.Token || current.Address != owner.Address {
			return false
		}
		probe, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		request, err := http.NewRequestWithContext(probe, "GET", "http://"+owner.Address+"/v1/status", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", "Bearer "+owner.Token)
		client := http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		response.Body.Close()
		return response.StatusCode == 200
	}
	lock, err := os.OpenFile(spec.Log+".owner.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		if !alive() {
			return errors.New("engine owner exited before startup")
		}
		err = filelock.Try(lock)
		if err == nil {
			break
		}
		if !filelock.Busy(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !alive() {
		return errors.New("engine owner exited before startup")
	}
	if err = protectEngineProcessTree(); err != nil {
		return err
	}
	child, err := startProcess(spec.Binary, spec.Args, spec.Log)
	if err != nil {
		return err
	}
	child.requestStop = func() bool { return stopEngineProtocol(spec) }
	defer child.stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-child.done:
			if child.err != nil {
				return fmt.Errorf("native engine exited: %w", child.err)
			}
			return nil
		case <-ticker.C:
			if alive() {
				failures = 0
			} else {
				failures++
				if failures >= 2 {
					return nil
				}
			}
		}
	}
}
