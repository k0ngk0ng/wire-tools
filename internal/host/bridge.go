package host

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// CommandRequest retains the existing terminal presentations and interactive
// dashboards. UI clients should use the resource routes for structured data.
// The compatibility adapter only invokes commands built into this executable.
// It never resolves a plugin or shell command from PATH.
type CommandRequest struct {
	Args      []string `json:"args"`
	Directory string   `json:"directory,omitempty"`
	Input     string   `json:"input,omitempty"`
}
type CommandEvent struct {
	Stream string `json:"stream,omitempty"`
	Data   string `json:"data,omitempty"`
	Exit   *int   `json:"exit,omitempty"`
}

func Bridge(home, version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		feature := r.PathValue("feature")
		switch feature {
		case "connect", "download", "talk":
		default:
			JSON(w, 404, map[string]string{"error": "unknown feature"})
			return
		}
		var request CommandRequest
		r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			JSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		// Lifecycle and update operations belong to the unified runtime. Do not
		// expose old per-plugin process or binary replacement entry points.
		for _, arg := range request.Args {
			if strings.HasPrefix(arg, "__") {
				JSON(w, 400, map[string]string{"error": "private command"})
				return
			}
		}
		if len(request.Args) > 0 && (request.Args[0] == "daemon" || request.Args[0] == "update") {
			JSON(w, 400, map[string]string{"error": "use wirectl update; feature runtimes become ready automatically"})
			return
		}
		// Reject component state overrides: one runtime must own every operation.
		for _, arg := range request.Args {
			if arg == "--state-dir" || strings.HasPrefix(arg, "--state-dir=") || arg == "--data-dir" || strings.HasPrefix(arg, "--data-dir=") {
				JSON(w, 400, map[string]string{"error": "select the unified state directory with WIRECTL_HOME or wirectl --state-dir"})
				return
			}
		}
		self, err := os.Executable()
		if err != nil {
			JSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		args := append([]string{"__" + feature}, request.Args...)
		cmd := exec.CommandContext(r.Context(), self, args...)
		cmd.Env = append(os.Environ(), "WIRECTL_HOME="+home, "WIRECTL_API_CLIENT=1", "WIRECTL_DOWNLOAD_HOME="+filepath.Join(home, "download"), "WIRE_TALK_HOME="+filepath.Join(home, "talk"), "WIRECTL_CONNECT_HOME="+filepath.Join(home, "connect"))
		if request.Directory != "" {
			if !filepath.IsAbs(request.Directory) {
				JSON(w, 400, map[string]string{"error": "directory must be absolute"})
				return
			}
			cmd.Dir = request.Directory
		}
		cmd.Stdin = bytes.NewBufferString(request.Input)
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Header().Set("X-Accel-Buffering", "no")
		var mu sync.Mutex
		emit := func(event CommandEvent) error {
			mu.Lock()
			defer mu.Unlock()
			err := json.NewEncoder(w).Encode(event)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			return err
		}
		cmd.Stdout = &eventWriter{stream: "stdout", emit: emit}
		cmd.Stderr = &eventWriter{stream: "stderr", emit: emit}
		code := 0
		if err = cmd.Run(); err != nil {
			var e *exec.ExitError
			if errors.As(err, &e) {
				code = e.ExitCode()
			} else {
				code = 1
				_ = emit(CommandEvent{Stream: "stderr", Data: fmt.Sprintln(err)})
			}
		}
		_ = emit(CommandEvent{Exit: &code})
	}
}

type eventWriter struct {
	stream string
	emit   func(CommandEvent) error
}

func (w *eventWriter) Write(p []byte) (int, error) {
	err := w.emit(CommandEvent{Stream: w.stream, Data: string(p)})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

// RenderCommand consumes a streamed command response without buffering an
// unbounded watch session or losing an underlying command's exit status.
func RenderCommand(response *http.Response, out, errOut io.Writer) error {
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("local API: %s", strings.TrimSpace(string(b)))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		var e CommandEvent
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return err
		}
		if e.Exit != nil {
			if *e.Exit != 0 {
				return &CommandError{Code: *e.Exit}
			}
			return nil
		}
		target := out
		if e.Stream == "stderr" {
			target = errOut
		}
		if _, err := io.WriteString(target, e.Data); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("local API command response ended before completion")
}

type CommandError struct{ Code int }

func (e *CommandError) Error() string { return fmt.Sprintf("command exited with status %d", e.Code) }
