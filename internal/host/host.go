// Package host owns the local API and the lifetimes of all feature resources.
package host

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/api"
	"github.com/k0ngk0ng/wire-tools/components/connect"
	"github.com/k0ngk0ng/wire-tools/components/download"
	"github.com/k0ngk0ng/wire-tools/components/talk"
	"github.com/k0ngk0ng/wire-tools/internal/state"
)

func Authenticate(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			JSON(w, 401, map[string]string{"error": "local API authentication required"})
			return
		}
		if strings.TrimSpace(r.Header.Get("Origin")) != "" {
			JSON(w, 403, map[string]string{"error": "browser origins are not accepted"})
			return
		}
		next.ServeHTTP(w, r)
	})
}
func JSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func Run(ctx context.Context, home, version string) error {
	if err := state.Dir(home); err != nil {
		return err
	}
	unlock, err := state.Lock(home)
	if err != nil {
		return err
	}
	defer unlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return err
	}
	ep := api.Endpoint{Address: listener.Addr().String(), Token: hex.EncodeToString(b), PID: os.Getpid(), Version: version}
	epPath := filepath.Join(home, "api.json")
	defer os.Remove(epPath)
	conn := connect.New(ctx, filepath.Join(home, "connect"), version)
	down := download.New(ctx, filepath.Join(home, "download"), version)
	voice := talk.New(ctx, filepath.Join(home, "talk"), version)
	defer conn.Close()
	defer down.Close()
	defer voice.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, 200, map[string]any{"version": version, "pid": os.Getpid(), "api_version": "v1", "features": []string{"connect", "download", "talk"}})
	})
	mux.HandleFunc("POST /v1/runtime/shutdown", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, 202, map[string]bool{"stopping": true})
		go cancel()
	})
	mux.Handle("/v1/connect/", http.StripPrefix("/v1/connect", conn))
	mux.Handle("/v1/download/", http.StripPrefix("/v1/download", down))
	mux.Handle("/v1/talk/", http.StripPrefix("/v1/talk", voice))
	mux.HandleFunc("POST /v1/commands/{feature}", Bridge(home, version))
	server := &http.Server{Handler: Authenticate(ep.Token, mux), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	data, _ := json.MarshalIndent(ep, "", "  ")
	stage, err := os.CreateTemp(home, ".api-*")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	if err = stage.Chmod(0600); err != nil {
		stage.Close()
		return err
	}
	if _, err = stage.Write(append(data, '\n')); err != nil {
		stage.Close()
		return err
	}
	if err = stage.Close(); err != nil {
		return err
	}
	// Only the holder of runtime.lock can replace or remove the endpoint.
	_ = os.Remove(epPath)
	if err = os.Rename(stagePath, epPath); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
	}
	// Resource teardown completes before endpoint removal and lock release.
	fmt.Fprintln(os.Stderr, "wirectl local API stopped")
	return nil
}
