package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/private"
)

// Shutdown failure matrix: a live shim must stop its native descendant; saved
// session must precede shutdown; missing/unsafe config must never trigger an
// unauthenticated request; and RPC errors must not expose secrets in argv.
func TestWatchdogFlushesAria2SessionBeforeProtocolShutdown(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	secret := strings.Repeat("a", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if len(request.Params) == 0 || string(request.Params[0]) != `"token:`+secret+`"` {
			t.Error("missing RPC authentication")
		}
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"wirectl","result":"OK"}`)
	}))
	defer server.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "aria2.conf")
	if err = os.WriteFile(path, []byte("rpc-listen-port="+port+"\nrpc-secret="+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = private.File(path); err != nil {
		t.Fatal(err)
	}
	spec := engineSpec{Binary: "a-chocolatey-shim.exe", Args: []string{"--conf-path=" + path}}
	if !stopEngineProtocol(spec) {
		t.Fatal("protocol shutdown failed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 2 || methods[0] != "aria2.saveSession" || methods[1] != "aria2.forceShutdown" {
		t.Fatalf("shutdown RPC order: %v", methods)
	}
	if strings.Contains(strings.Join(spec.Args, " "), secret) {
		t.Fatal("RPC secret leaked into process arguments")
	}
}

func TestWatchdogRejectsMissingOrUnauthenticatedRPCConfig(t *testing.T) {
	for _, body := range []string{"rpc-listen-port=1\n", "rpc-listen-port=invalid\nrpc-secret=" + strings.Repeat("a", 64), "rpc-listen-port=1\nrpc-secret=\n"} {
		path := filepath.Join(t.TempDir(), "aria2.conf")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := private.File(path); err != nil {
			t.Fatal(err)
		}
		if stopEngineProtocol(engineSpec{Args: []string{"--conf-path=" + path}}) {
			t.Fatal("unsafe RPC configuration accepted")
		}
	}
}
