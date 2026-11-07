package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/video"
)

func init() {
	if os.Getenv("WIRE_DAEMON_VIDEO_HELPER") != "1" || !strings.HasPrefix(filepath.Base(os.Args[0]), "yt-dlp-daemon-fixture") {
		return
	}
	for _, arg := range os.Args[1:] {
		if arg == "--dump-single-json" {
			if os.Getenv("WIRE_DAEMON_VIDEO_SLOW") == "1" {
				time.Sleep(time.Second)
			}
			fmt.Println(`{"entries":[{"id":"101","formats":[{}]},{"id":"102","formats":[{}]}]}`)
			os.Exit(0)
		}
	}
	// Downloads stay alive long enough to exercise pause/remove deterministically.
	// Backend cancellation terminates this direct native child on every OS.
	time.Sleep(5 * time.Second)
	os.Exit(1)
}

func videoStore(t *testing.T) (*Store, *video.Backend, string) {
	t.Helper()
	dir := t.TempDir()
	name := "yt-dlp-daemon-fixture"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	script := filepath.Join(dir, name)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(script, binary, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRE_DAEMON_VIDEO_HELPER", "1")
	opts := video.Options{Binary: script, FFmpeg: script, Deno: script, StateDir: dir, Downloads: filepath.Join(dir, "downloads"), Max: 1}
	b := video.New(opts)
	t.Cleanup(func() { b.Close() })
	s, err := NewStore(dir, map[string]engine.Backend{"yt-dlp": b})
	if err != nil {
		t.Fatal(err)
	}
	return s, b, dir
}
func TestVideoSubmissionDedupeAndPersistence(t *testing.T) {
	s, _, dir := videoStore(t)
	ctx := context.Background()
	first, err := s.Submit(ctx, "https://x.com/a/status/123?s=20")
	if err != nil || len(first.Jobs) != 2 {
		t.Fatalf("%+v %v", first, err)
	}
	second, err := s.Submit(ctx, "https://twitter.com/b/status/123/video/2")
	if err != nil || len(second.Jobs) != 2 {
		t.Fatalf("%+v %v", second, err)
	}
	for i, j := range first.Jobs {
		if j.ID != second.Jobs[i].ID {
			t.Fatal("duplicate media task")
		}
		if j.Video == nil || j.Video.MediaID == "" {
			t.Fatal("missing media identity")
		}
	}
	if err = s.Action(ctx, first.Jobs[0].ID, "pause"); err != nil {
		t.Fatal(err)
	}
	// Store persisted media identity and paused state, with no signed URLs.
	restored, err := NewStore(dir, map[string]engine.Backend{})
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := restored.Snapshot()
	if len(jobs) != 2 || jobs[0].Status != "paused" || jobs[0].Video.MediaID != "101" {
		t.Fatalf("%+v", jobs)
	}
	if err = s.Action(ctx, first.Jobs[0].ID, "remove"); err != nil {
		t.Fatal(err)
	}
	third, err := s.Submit(ctx, "https://x.com/a/status/123")
	if err != nil {
		t.Fatal(err)
	}
	if third.Jobs[0].ID == first.Jobs[0].ID || third.Jobs[1].ID != first.Jobs[1].ID {
		t.Fatal("removed item resubmission affected sibling")
	}
}
func TestVideoSubmissionHTTPAndNoHTMLFallback(t *testing.T) {
	s, _, _ := videoStore(t)
	h := Handler(s, func() {})
	request := func(path, source string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"source": source})
		req := httptest.NewRequest(http.MethodPost, "http://localhost"+path, strings.NewReader(string(body)))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	w := request("/v1/submissions", "https://x.com/a/status/123")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result Submission
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || len(result.Jobs) != 2 {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{"/v1/submissions", "/v1/jobs"} {
		w = request(path, "https://x.com/home")
		if w.Code != 422 {
			t.Fatalf("%s %d %s", path, w.Code, w.Body.String())
		}
	}
}
func TestVideoResolutionDoesNotLockStore(t *testing.T) {
	s, _, _ := videoStore(t)
	t.Setenv("WIRE_DAEMON_VIDEO_SLOW", "1")
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.Submit(context.Background(), "https://x.com/a/status/123") }()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	s.Snapshot()
	if time.Since(start) > 200*time.Millisecond {
		t.Fatal("resolution held store lock")
	}
	<-done
}
