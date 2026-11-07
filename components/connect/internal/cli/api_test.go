package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Failure cases: API failure must propagate; CLI must not fall back to a
// native service or another state directory when its API is unavailable.
func TestAPICommandUsesConnectionResources(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
			t.Error("missing capability token")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/status" {
			w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path == "/v1/connect/connections" {
			w.Write([]byte(`{"connections":[{"name":"office","state":"stopped","status":{"mode":"stopped"}}]}`))
			return
		}
		t.Error(r.URL.Path)
	}))
	defer server.Close()
	dir := t.TempDir()
	b, _ := json.Marshal(map[string]string{"address": strings.TrimPrefix(server.URL, "http://"), "token": strings.Repeat("a", 64)})
	if err := os.WriteFile(filepath.Join(dir, "api.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRECTL_HOME", dir)
	var out, errOut bytes.Buffer
	handled, err := RunAPI(context.Background(), []string{"status", "--all", "--json"}, "test", strings.NewReader(""), &out, &errOut)
	if err != nil || !handled {
		t.Fatal(handled, err)
	}
	if !strings.Contains(out.String(), "office") {
		t.Fatal(out.String())
	}
	if len(paths) != 2 || paths[1] != "GET /v1/connect/connections" {
		t.Fatal(paths)
	}
}
