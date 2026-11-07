package talk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
)

func request(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestConfigurationAPIAndValidation(t *testing.T) {
	m := New(context.Background(), t.TempDir(), "test")
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	w := request(t, m, "POST", "/mute", nil)
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "daemon") {
		t.Fatalf("missing config: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "POST", "/config", map[string]any{"listen": "127.0.0.1:0", "input": "none"})
	if w.Code != http.StatusCreated {
		t.Fatalf("config create: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "GET", "/mute", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET mutation: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "GET", "/status", nil)
	if !strings.Contains(w.Body.String(), `"active":false`) {
		t.Fatalf("invalid method started audio: %s", w.Body)
	}
	w = request(t, m, "GET", "/config", nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"key"`) {
		t.Fatalf("configuration leaked key: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "POST", "/config", map[string]any{"listen": "127.0.0.1:0"})
	if w.Code != http.StatusConflict {
		t.Fatalf("config overwritten: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "POST", "/unknown", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown route: %d", w.Code)
	}
}

func TestActionsAutomaticallyCreateOneSession(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), dir, "test-version")
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Error(err)
		}
	})
	w := request(t, m, "POST", "/config", map[string]any{"listen": "127.0.0.1:0", "input": "none"})
	if w.Code != http.StatusCreated {
		t.Fatalf("config: %d %s", w.Code, w.Body)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			w := request(t, m, "POST", "/mute", nil)
			if w.Code != http.StatusNoContent {
				t.Errorf("auto mute: %d %s", w.Code, w.Body)
			}
		})
	}
	wg.Wait()
	w = request(t, m, "GET", "/status", nil)
	var status map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || status["muted"] != true || status["version"] != "test-version" {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "POST", "/unmute", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("unmute: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "DELETE", "/session", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("session stop: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "GET", "/status", nil)
	if !strings.Contains(w.Body.String(), `"active":false`) {
		t.Fatalf("stopped: %s", w.Body)
	}
	w = request(t, m, "POST", "/mute", nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("auto resume: %d %s", w.Code, w.Body)
	}
}

func TestInvitationAndPairResources(t *testing.T) {
	inviterDir, peerDir := t.TempDir(), t.TempDir()
	inviter, peer := New(context.Background(), inviterDir, "test"), New(context.Background(), peerDir, "test")
	t.Cleanup(func() { inviter.Close(); peer.Close() })
	// A fixed port can be chosen from an actual live session's UDP address.
	w := request(t, inviter, "POST", "/config", map[string]any{"listen": "127.0.0.1:0", "input": "none"})
	if w.Code != http.StatusCreated {
		t.Fatalf("config: %s", w.Body)
	}
	w = request(t, inviter, "POST", "/session", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	w = request(t, inviter, "POST", "/invitations", map[string]any{})
	if w.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", w.Code, w.Body)
	}
	var invitation struct{ Address, Code string }
	if err := json.Unmarshal(w.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(peer)
	defer s.Close()
	b, _ := json.Marshal(map[string]any{"address": invitation.Address, "code": invitation.Code, "listen": "127.0.0.1:0", "input": "none"})
	response, err := http.Post(s.URL+"/pair", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	output, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("pair: %d %s", response.StatusCode, output)
	}
	a, err := config.Load(inviterDir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := config.Load(peerDir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Key != other.Key {
		t.Fatal("pair API did not persist same authenticated room key")
	}
}

func TestModeCanChangeAfterLeavingSession(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), dir, "test")
	t.Cleanup(func() { m.Close() })
	w := request(t, m, "POST", "/config", map[string]any{"listen": "127.0.0.1:0", "input": "none"})
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "POST", "/groups", map[string]any{"action": "mode", "mode": "direct"})
	if w.Code != 200 {
		t.Fatalf("mode while inactive: %d %s", w.Code, w.Body)
	}
	c, err := config.Load(dir)
	if err != nil || c.Mode != "direct" {
		t.Fatalf("mode not saved: %v %+v", err, c)
	}
	w = request(t, m, "POST", "/session", nil)
	if w.Code != 200 {
		t.Fatalf("session: %d %s", w.Code, w.Body)
	}
	w = request(t, m, "POST", "/groups", map[string]any{"action": "mode", "mode": "room"})
	if w.Code != 409 {
		t.Fatalf("mode changed on active session: %d %s", w.Code, w.Body)
	}
}
