package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClientRejectsUnsafeEndpoint(t *testing.T) {
	for _, address := range []string{"example.com:80", "0.0.0.0:80", "127.0.0.1:80/path", "127.0.0.1:80@evil"} {
		t.Run(address, func(t *testing.T) {
			dir := t.TempDir()
			data, _ := json.Marshal(Endpoint{Address: address, Token: strings.Repeat("a", 64)})
			os.WriteFile(filepath.Join(dir, "api.json"), data, 0600)
			_, err := New(dir).Endpoint()
			if err == nil {
				t.Fatal("unsafe endpoint accepted")
			}
		})
	}
}
func TestClientDirectRequestAuthenticationAndError(t *testing.T) {
	token := strings.Repeat("b", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("missing bearer")
		}
		if r.URL.Path == "/bad" {
			w.WriteHeader(422)
			w.Write([]byte(`{"error":"invalid source"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	data, _ := json.Marshal(Endpoint{Address: strings.TrimPrefix(server.URL, "http://"), Token: token})
	os.WriteFile(filepath.Join(dir, "api.json"), data, 0600)
	var result struct {
		OK bool `json:"ok"`
	}
	client := New(dir)
	if err := client.RequestExisting(context.Background(), "GET", "/status", nil, &result); err != nil || !result.OK {
		t.Fatalf("%+v %v", result, err)
	}
	if err := client.RequestExisting(context.Background(), "GET", "/bad", nil, nil); err == nil || !strings.Contains(err.Error(), "invalid source") {
		t.Fatal(err)
	}
}
