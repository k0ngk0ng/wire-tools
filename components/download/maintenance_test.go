package download

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/auth"
)

func TestManagedTrackerEditsWhileRuntimeOwnsState(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), dir, "test")
	defer m.Close()
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body)))
		return rec
	}
	if r := call("GET", "/status", ""); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	tracker := "http://127.0.0.1:12345/announce"
	r := call("POST", "/trackers", `{"operation":"add","trackers":["`+tracker+`"]}`)
	if r.Code != 200 {
		t.Fatalf("tracker edit under runtime ownership: %d %s", r.Code, r.Body.String())
	}
	r = call("GET", "/trackers", "")
	if r.Code != 200 || !bytes.Contains(r.Body.Bytes(), []byte(tracker)) {
		t.Fatalf("%d %s", r.Code, r.Body.String())
	}
	before, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	r = call("PUT", "/trackers", `{"trackers":["invalid://tracker"]}`)
	if r.Code != 400 {
		t.Fatalf("invalid tracker: %d %s", r.Code, r.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("invalid tracker changed configuration")
	}
	r = call("POST", "/trackers", `{"operation":"remove","trackers":["`+tracker+`"]}`)
	if r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	r = call("GET", "/trackers", "")
	if bytes.Contains(r.Body.Bytes(), []byte(tracker)) {
		t.Fatal("tracker not removed")
	}
}

func TestAuthSessionResourcesSaveAndForgetWithoutCookieResponse(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), dir, "test")
	defer m.Close()
	session := auth.Session{Origin: "https://example.com", UserAgent: "test-browser", Cookies: []auth.Cookie{{Name: "sid", Value: "secret-cookie", Domain: "example.com", Path: "/", Secure: true}}}
	data, _ := json.Marshal(session)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/auth/sessions", bytes.NewReader(data)))
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("secret-cookie")) {
		t.Fatal("cookie leaked in response")
	}
	saved, err := auth.Load(dir, session.Origin)
	if err != nil || saved == nil || saved.Cookies[0].Value != "secret-cookie" {
		t.Fatalf("saved session %v %v", saved, err)
	}
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("DELETE", "http://localhost/auth/sessions?origin="+url.QueryEscape(session.Origin), nil))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	saved, err = auth.Load(dir, session.Origin)
	if err != nil || saved != nil {
		t.Fatal("session was not forgotten")
	}
}
