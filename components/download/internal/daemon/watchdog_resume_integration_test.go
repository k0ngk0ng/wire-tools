package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/private"
)

// This fixture deliberately delays native shutdown long enough for the fast
// HTTP transfer to finish. The shutdown boundary must freeze it first, keeping
// the same GID recoverable and an independently user-paused task paused.
func TestRealAria2WatchdogFreezesBeforeDelayedShutdown(t *testing.T) {
	binary := os.Getenv("WIRECTL_TEST_ARIA2")
	if binary == "" {
		binary, _ = exec.LookPath("aria2c")
	}
	if binary == "" {
		t.Skip("real aria2 engine is not installed")
	}
	root := t.TempDir()
	session := filepath.Join(root, "session.txt")
	if err := os.WriteFile(session, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var sent atomic.Int64
	payload := make([]byte, 4<<20)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		if value := r.Header.Get("Range"); strings.HasPrefix(value, "bytes=") {
			offset, _ = strconv.Atoi(strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)[0])
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-offset))
		w.Header().Set("Accept-Ranges", "bytes")
		if offset > 0 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)))
			w.WriteHeader(206)
		}
		for ; offset < len(payload); offset += 8192 {
			n, err := w.Write(payload[offset:min(offset+8192, len(payload))])
			if r.URL.Path == "/active.bin" {
				sent.Add(int64(n))
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}))
	defer source.Close()
	port := testFreeTCPPort(t)
	secret := strings.Repeat("a", 64)
	nativeConfig := filepath.Join(root, "aria2.conf")
	body := []string{"enable-rpc=true", "rpc-listen-all=false", "rpc-listen-port=" + strconv.Itoa(port), "rpc-secret=" + secret, "dir=" + root, "input-file=" + session, "save-session=" + session, "force-save=false", "continue=true", "file-allocation=none", "allow-overwrite=false", "auto-file-renaming=false", "enable-dht=false", "enable-dht6=false", "enable-peer-exchange=false", "bt-enable-lpd=false", "listen-port=" + strconv.Itoa(testFreeTCPPort(t)), "summary-interval=0", "console-log-level=error"}
	if err := os.WriteFile(nativeConfig, []byte(strings.Join(body, "\n")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := private.File(nativeConfig); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(root, "aria2.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	child := exec.Command(binary, "--conf-path="+nativeConfig)
	child.Stdout = log
	child.Stderr = log
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = child.Wait(); close(done) }()
	aria := engine.NewAria2(port, secret)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = aria.Call(ctx, "forceShutdown", nil, nil)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = child.Process.Kill()
			<-done
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = aria.Call(ctx, "getVersion", nil, nil)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native RPC did not become ready", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	pausedID := "fedcba9876543210"
	if err = aria.Call(context.Background(), "addUri", []any{[]string{source.URL + "/paused.bin"}, map[string]string{"gid": pausedID, "pause": "true"}}, nil); err != nil {
		t.Fatal(err)
	}
	activeID, err := aria.AddWithID(context.Background(), source.URL+"/active.bin", "0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); sent.Load() < 32768; {
		if time.Now().After(deadline) {
			t.Fatal("native transfer did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		if strings.Contains(string(data), "aria2.forceShutdown") {
			time.Sleep(1500 * time.Millisecond)
		}
		response, err := http.Post(aria.URL, "application/json", strings.NewReader(string(data)))
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	proxyPort := strings.Split(strings.TrimPrefix(proxy.URL, "http://"), ":")[1]
	shutdownConfig := filepath.Join(root, "shutdown.conf")
	if err = os.WriteFile(shutdownConfig, []byte("rpc-listen-port="+proxyPort+"\nrpc-secret="+secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = private.File(shutdownConfig); err != nil {
		t.Fatal(err)
	}
	if !stopEngineProtocol(engineSpec{Args: []string{"--conf-path=" + shutdownConfig}}) {
		t.Fatal("protocol shutdown failed")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native process did not stop")
	}
	saved, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), activeID) {
		t.Fatalf("active GID pruned during delayed shutdown: %q", saved)
	}
	if !strings.Contains(string(saved), pausedID) {
		t.Fatalf("user-paused GID lost: %q", saved)
	}
	if sent.Load() >= int64(len(payload)) {
		t.Fatal("fast transfer continued to completion during delayed shutdown")
	}
	intent, err := readAria2ResumeIntent(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.GIDs) != 1 || intent.GIDs[0] != activeID {
		t.Fatalf("incorrect resume intent: %+v", intent)
	}
	restarted := exec.Command(binary, "--conf-path="+nativeConfig)
	restarted.Stdout = log
	restarted.Stderr = log
	if err = restarted.Start(); err != nil {
		t.Fatal(err)
	}
	restartedDone := make(chan struct{})
	go func() { _ = restarted.Wait(); close(restartedDone) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = aria.Call(ctx, "forceShutdown", nil, nil)
		cancel()
		select {
		case <-restartedDone:
		case <-time.After(5 * time.Second):
			_ = restarted.Process.Kill()
			<-restartedDone
		}
	}()
	restore, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = resumeAria2AfterShutdown(restore, aria, root); err != nil {
		t.Fatal(err)
	}
	var paused aria2TaskState
	if err = aria.Call(restore, "tellStatus", []any{pausedID}, &paused); err != nil || paused.Status != "paused" {
		t.Fatalf("user pause lost: %+v %v", paused, err)
	}
	for {
		var active aria2TaskState
		if err = aria.Call(restore, "tellStatus", []any{activeID}, &active); err != nil {
			t.Fatal(err)
		}
		if active.Status == "complete" {
			break
		}
		if active.Status == "error" || active.Status == "paused" {
			var detail map[string]any
			_ = aria.Call(restore, "tellStatus", []any{activeID}, &detail)
			t.Fatalf("previously active task did not resume: %+v", detail)
		}
		select {
		case <-restore.Done():
			t.Fatal("resumed native transfer did not complete")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = os.Stat(filepath.Join(root, aria2ResumeFile)); !os.IsNotExist(err) {
		t.Fatal("intent marker was not retired")
	}

}
