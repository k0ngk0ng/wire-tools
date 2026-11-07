package daemon

import (
	"context"
	"errors"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
	"testing"
)

// Shutdown failure matrix: engine completion between polling ticks; querying a
// backend after close; persistence errors; and idle backends during teardown.
// This fixture isolates the first two without network timing dependencies.
type finishingBackend struct{ closed bool }

func (b *finishingBackend) Add(context.Context, string) (string, error) { return "final-task", nil }
func (b *finishingBackend) List(context.Context) ([]engine.Item, error) {
	if b.closed {
		return nil, errors.New("engine already closed")
	}
	return []engine.Item{{ID: "final-task", Name: "result.bin", Status: "complete", Progress: 100, Total: 10, Completed: 10}}, nil
}
func (b *finishingBackend) Pause(context.Context, string) error  { return nil }
func (b *finishingBackend) Resume(context.Context, string) error { return nil }
func (b *finishingBackend) Remove(context.Context, string) error { return nil }
func (b *finishingBackend) Close() error                         { b.closed = true; return nil }
func TestManagedShutdownSnapshotsBeforeClosingEngines(t *testing.T) {
	dir := t.TempDir()
	backend := &finishingBackend{}
	store, err := NewStore(dir, map[string]engine.Backend{"aria2": backend})
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Add(context.Background(), "https://example.invalid/result.bin")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	searches, err := NewSearchService(ctx, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	m := &Managed{Store: store, cancel: cancel, done: done, backends: []engine.Backend{backend}, searches: searches, stateLock: lock}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(dir, map[string]engine.Backend{})
	if err != nil {
		t.Fatal(err)
	}
	jobs, _ := reloaded.Snapshot()
	if len(jobs) != 1 || jobs[0].ID != job.ID || jobs[0].Status != "complete" {
		t.Fatalf("completion lost during shutdown: %+v", jobs)
	}
}
