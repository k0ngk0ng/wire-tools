// Package control provides a loopback-only, capability-authenticated API.
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/k0ngk0ng/wire-tools/api"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/private"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Endpoint struct {
	Address string `json:"address"`
	Token   string `json:"token"`
}
type Server struct {
	server   *http.Server
	listener net.Listener
	path     string
	endpoint Endpoint
}

func Start(dir string, status func() any, stop func(), mute func(bool), muteOutput func(bool), media ...http.Handler) (*Server, error) {
	if err := private.Dir(dir); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "control.json")
	// A separate OS-held lock is acquired by the caller before this function.
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		l.Close()
		return nil, err
	}
	s := &Server{listener: l, path: path, endpoint: Endpoint{Address: l.Addr().String(), Token: hex.EncodeToString(token)}}
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(status())
	})
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		// Complete the empty response before teardown closes this connection.
		// Without a length, Flush selects chunked encoding and Close can omit
		// its final chunk, making a successful stop look like unexpected EOF.
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(202)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		stop()
	})
	mux.HandleFunc("/mute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		mute(true)
		w.WriteHeader(204)
	})
	mux.HandleFunc("/unmute", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		mute(false)
		w.WriteHeader(204)
	})
	for path, muted := range map[string]bool{"/mute/output": true, "/unmute/output": false} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			muteOutput(muted)
			w.WriteHeader(204)
		})
	}
	if len(media) > 0 {
		mux.Handle("/media", media[0])
	}
	if len(media) > 1 {
		mux.Handle("/groups", media[1])
	}
	if len(media) > 2 {
		mux.Handle("/call", media[2])
	}
	s.server = &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.endpoint.Token)) != 1 {
			w.WriteHeader(401)
			return
		}
		mux.ServeHTTP(w, r)
	})}
	b, _ := json.Marshal(s.endpoint)
	if err = os.WriteFile(path, b, 0600); err != nil {
		l.Close()
		return nil, err
	}
	go s.server.Serve(l)
	return s, nil
}
func (s *Server) Close() { s.server.Close(); os.Remove(s.path) }
func Request(ctx context.Context, dir, method, path string) ([]byte, error) {
	return RequestBody(ctx, dir, method, path, nil)
}

type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string { return e.Message }

func RequestBody(ctx context.Context, dir, method, path string, data []byte) ([]byte, error) {
	if os.Getenv("WIRECTL_API_CLIENT") == "1" {
		return api.New(os.Getenv("WIRECTL_HOME")).Do(ctx, method, "/v1/talk"+path, data)
	}
	return RequestDirectBody(ctx, dir, method, path, data)
}

// RequestDirectBody is runtime-internal forwarding and never re-enters the host.
func RequestDirectBody(ctx context.Context, dir, method, path string, data []byte) ([]byte, error) {
	b, err := os.ReadFile(filepath.Join(dir, "control.json"))
	if err != nil {
		return nil, fmt.Errorf("talk is not running: %w", err)
	}
	var ep Endpoint
	if err = json.Unmarshal(b, &ep); err != nil {
		return nil, err
	}
	host, _, err := net.SplitHostPort(ep.Address)
	if err != nil || host != "127.0.0.1" || len(ep.Token) != 64 {
		return nil, errors.New("invalid local control endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+ep.Address+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	client := http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("talk is not running or unavailable: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("control: %s %s", resp.Status, strings.TrimSpace(string(body)))}
	}
	return body, nil
}
