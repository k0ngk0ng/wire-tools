package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/auth"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/filelock"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These tests exercise the public resource API, HTTP transfer and durable state
// with no separately installed engines, processes or manual initialization.
func TestAPIHTTPDownloadAndRecovery(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	payload := bytes.Repeat([]byte("download api evidence\n"), 32768)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer source.Close()
	m := New(context.Background(), dir, "test")
	api := httptest.NewServer(m)
	defer api.Close()
	post := func(target string) map[string]any {
		t.Helper()
		resp, err := http.Post(api.URL+"/jobs", "application/json", bytes.NewBufferString(`{"source":"`+target+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 201 {
			t.Fatalf("submit %d: %s", resp.StatusCode, data)
		}
		var job map[string]any
		if err = json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
		return job
	}
	job := post(source.URL + "/evidence.bin")
	deadline := time.Now().Add(10 * time.Second)
	var status struct{ Jobs []struct{ ID, Status string } }
	for time.Now().Before(deadline) {
		resp, err := http.Get(api.URL + "/status")
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(resp.Body).Decode(&status)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(status.Jobs) > 0 && status.Jobs[0].Status == "complete" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(status.Jobs) != 1 || status.Jobs[0].Status != "complete" {
		t.Fatalf("unfinished: %+v", status)
	}
	data, err := os.ReadFile(filepath.Join(dir, "downloads", "evidence.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(data) != sha256.Sum256(payload) {
		t.Fatal("download content differs")
	}
	if duplicate := post(source.URL + "/evidence.bin"); duplicate["id"] != job["id"] {
		t.Fatal("duplicate job created")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	next := New(context.Background(), dir, "next")
	defer next.Close()
	rec := httptest.NewRecorder()
	next.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/status", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"status":"complete"`)) {
		t.Fatal(rec.Body.String())
	}
}

func TestMissingOptionalEngineIsIsolated(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := New(context.Background(), t.TempDir(), "test")
	defer m.Close()
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/jobs", bytes.NewBufferString(`{"source":"ed2k://|file|a.bin|1|0123456789abcdef0123456789abcdef|/"}`)))
	if rec.Code < 400 {
		t.Fatalf("missing engine accepted: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/status", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
}

func TestInvalidDownloadDoesNotCreateTask(t *testing.T) {
	m := New(context.Background(), t.TempDir(), "test")
	defer m.Close()
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/jobs", bytes.NewBufferString(`{"source":"invalid://source"}`)))
	if rec.Code != 422 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/status", nil))
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"jobs":[]`)) {
		t.Fatal(rec.Body.String())
	}
}

func TestAuthenticatedHTTPKeepsOriginalSource(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("sid")
		if err != nil || cookie.Value != "private-cookie" {
			http.Error(w, "login required", 401)
			return
		}
		w.Write([]byte("authenticated transfer"))
	}))
	defer source.Close()
	if err := auth.Save(dir, auth.Session{Origin: source.URL, UserAgent: "test-browser", Cookies: []auth.Cookie{{Name: "sid", Value: "private-cookie", Domain: "127.0.0.1", Path: "/"}}}); err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), dir, "test")
	defer m.Close()
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("POST", "http://localhost/jobs", bytes.NewBufferString(`{"source":"`+source.URL+`/private.bin"}`)))
	if rec.Code != 201 {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec = httptest.NewRecorder()
		m.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/status", nil))
		if bytes.Contains(rec.Body.Bytes(), []byte(`"status":"complete"`)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, err := os.ReadFile(filepath.Join(dir, "downloads", "private.bin"))
	if err != nil || string(data) != "authenticated transfer" {
		t.Fatalf("content %q, %v; status %s", data, err, rec.Body.String())
	}
	journal, err := os.ReadFile(filepath.Join(dir, "http-jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(journal, []byte(source.URL+"/private.bin")) || bytes.Contains(journal, []byte("private-cookie")) || bytes.Contains(journal, []byte("/fetch/")) {
		t.Fatal("HTTP journal did not retain a safe original source")
	}
}

func TestLegacyStateLockExcludesManagedRuntimeWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	if _, err := config.Init(dir, filepath.Join(dir, "downloads")); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = filelock.Try(lock); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(context.Background(), dir, "test")
	defer m.Close()
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/status", nil))
	if rec.Code != 503 {
		t.Fatalf("legacy ownership not respected: %d %s", rec.Code, rec.Body.String())
	}
	after, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("legacy configuration modified")
	}
	afterEntries, _ := os.ReadDir(dir)
	if len(afterEntries) != len(entries) {
		t.Fatal("legacy state gained runtime files while locked")
	}
}

func TestManagedRuntimeHoldsLegacyLockUntilClose(t *testing.T) {
	dir := t.TempDir()
	m := New(context.Background(), dir, "test")
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest("GET", "http://localhost/status", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	lock, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = filelock.Try(lock); !filelock.Busy(err) {
		t.Fatalf("managed runtime did not hold state lock: %v", err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	if err = filelock.Try(lock); err != nil {
		t.Fatalf("managed close did not release state: %v", err)
	}
}
