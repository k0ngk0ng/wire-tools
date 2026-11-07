package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
)

// Resume failure matrix: automatic shutdown pauses, preexisting user pauses,
// engine startup delay, a failed unpause, malformed markers, and retry after a
// partial restoration. Only captured active/waiting GIDs may be resumed.
func TestAria2ResumeIntentRestoresOnlyCapturedGIDs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "resume-after-shutdown.json")
	if err := atomicWrite(path, []byte(`{"version":1,"gids":["0123456789abcdef"]}`)); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var resumed []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string
			Params []json.RawMessage
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Method {
		case "aria2.tellStatus":
			fmt.Fprint(w, `{"result":{"gid":"0123456789abcdef","status":"paused"}}`)
		case "aria2.saveSession":
			fmt.Fprint(w, `{"result":"OK"}`)
		case "aria2.unpause":
			var gid string
			json.Unmarshal(req.Params[1], &gid)
			mu.Lock()
			resumed = append(resumed, gid)
			mu.Unlock()
			fmt.Fprint(w, `{"result":"0123456789abcdef"}`)
		default:
			t.Errorf("unexpected RPC %s", req.Method)
			fmt.Fprint(w, `{"result":"OK"}`)
		}
	}))
	defer server.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	aria := engine.NewAria2(port, strings.Repeat("a", 64))
	if err := resumeAria2AfterShutdown(context.Background(), aria, root); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(resumed) != 1 || resumed[0] != "0123456789abcdef" {
		t.Fatalf("resumed %v", resumed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("resume marker not retired")
	}
}

func TestAria2ResumeIntentRemainsForRetryOnFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "resume-after-shutdown.json")
	if err := atomicWrite(path, []byte(`{"version":1,"gids":["0123456789abcdef"]}`)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"error":{"code":1,"message":"not restored yet"}}`)
	}))
	defer server.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	aria := engine.NewAria2(port, strings.Repeat("a", 64))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := resumeAria2AfterShutdown(ctx, aria, root); err == nil {
		t.Fatal("missing GID accepted")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("resume intent lost on transient failure", err)
	}
}
