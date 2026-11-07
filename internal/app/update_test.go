package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k0ngk0ng/wire-tools/api"
)

// Exit failure matrix: stale endpoint after process exit, an old owner still
// flushing, and a new runtime already taking ownership. Endpoint deletion is
// not an ownership signal on Windows, where readers can delay file removal.
func TestUpdateAcceptsReleasedRuntimeWithStaleEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/update" {
			json.NewEncoder(w).Encode(map[string]string{"version": "fixture"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"version": "fixture", "pid": 123})
	}))
	defer server.Close()
	home := t.TempDir()
	ep, _ := json.Marshal(api.Endpoint{Address: strings.TrimPrefix(server.URL, "http://"), Token: strings.Repeat("a", 64), PID: 123})
	if err := os.WriteFile(filepath.Join(home, "api.json"), ep, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := updateCommand(ctx, api.New(home), nil); err != nil {
		t.Fatalf("stale endpoint was treated as a live runtime: %v", err)
	}
}

func TestConnectSetupHelpDoesNotReceiveStateFlags(t *testing.T) {
	t.Setenv("WIRECTL_HOME", t.TempDir())
	if err := Run(context.Background(), []string{"connect", "setup", "--help"}, "fixture"); err != nil {
		t.Fatalf("public setup routing corrupted arguments: %v", err)
	}
}
