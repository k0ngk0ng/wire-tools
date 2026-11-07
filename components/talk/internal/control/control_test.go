package control

import (
	"context"
	"net/http"
	"testing"
)

func TestLockExclusiveAndRelease(t *testing.T) {
	dir := t.TempDir()
	release, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := Lock(dir); err == nil {
		second()
		t.Fatal("two sessions acquired the same profile")
	}
	release()
	third, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	third()
}
func TestControlRequiresCapabilityAndSeparatesWatchFromStop(t *testing.T) {
	dir := t.TempDir()
	stopped := make(chan struct{}, 1)
	muted := make(chan bool, 2)
	outputMuted := make(chan bool, 2)
	s, err := Start(dir, func() any { return map[string]bool{"online": true} }, func() { stopped <- struct{}{} }, func(m bool) { muted <- m }, func(m bool) { outputMuted <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	resp, err := http.Get("http://" + s.endpoint.Address + "/stop")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal(resp.Status)
	}
	if _, err = Request(context.Background(), dir, "GET", "/status"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
		t.Fatal("status stopped session")
	default:
	}
	for _, path := range []string{"/mute", "/unmute"} {
		if _, err = Request(context.Background(), dir, "POST", path); err != nil {
			t.Fatal(err)
		}
	}
	if !<-muted || <-muted {
		t.Fatal("mute controls")
	}
	for _, path := range []string{"/mute/output", "/unmute/output"} {
		if _, err = Request(context.Background(), dir, "GET", path); err == nil {
			t.Fatal("GET changed mute state")
		}
		if _, err = Request(context.Background(), dir, "POST", path); err != nil {
			t.Fatal(err)
		}
	}
	if !<-outputMuted || <-outputMuted {
		t.Fatal("output mute controls")
	}
	if len(muted) != 0 {
		t.Fatal("output control changed input")
	}
	if _, err = Request(context.Background(), dir, "POST", "/stop"); err != nil {
		t.Fatal(err)
	}
	<-stopped
}

func TestStopResponseCompletesBeforeImmediateShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		dir := t.TempDir()
		server := make(chan *Server, 1)
		closed := make(chan struct{})
		s, err := Start(dir, func() any { return nil }, func() { (<-server).Close(); close(closed) }, func(bool) {}, func(bool) {})
		if err != nil {
			t.Fatal(err)
		}
		server <- s
		_, err = Request(context.Background(), dir, "POST", "/stop")
		<-closed
		if err != nil {
			t.Fatalf("successful shutdown returned %v", err)
		}
	}
}
