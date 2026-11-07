package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/search"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/video"
)

// Managed embeds downloads in the unified local API. Expensive native engines
// are opened only when their protocol is requested or has unfinished tasks.
type Managed struct {
	Handler    http.Handler
	Store      *Store
	cancel     context.CancelFunc
	done       chan struct{}
	closeOnce  sync.Once
	closeErr   error
	backends   []engine.Backend
	searches   *SearchService
	closeProxy func()
}

func OpenManaged(ctx context.Context, dir string, c config.Config) (*Managed, error) {
	// Allocate engine/control ports for this runtime without rewriting the saved
	// user configuration. Separate state directories may run concurrently.
	for _, port := range []*int{&c.Aria2Port, &c.AMulePort} {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		*port = l.Addr().(*net.TCPAddr).Port
		l.Close()
	}
	c.AuthProxyPort = 0
	if err := prepare(dir, c, false); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	m := &Managed{cancel: cancel, done: make(chan struct{})}
	builtin, err := engine.NewHTTP(ctx, c.Downloads, dir)
	if err != nil {
		cancel()
		return nil, err
	}
	_, ariaAvailable := exec.LookPath(c.Aria2Binary)
	aria := &lazyBackend{factory: func(ctx context.Context) (engine.Backend, func(), error) {
		if _, err := exec.LookPath(c.Aria2Binary); err != nil {
			return nil, nil, fmt.Errorf("BitTorrent requires the aria2 engine; install the complete wirectl distribution: %w", err)
		}
		if err := availablePort(c.Aria2Port); err != nil {
			return nil, nil, err
		}
		p, err := startOwnedProcess(c.Aria2Binary, []string{"--conf-path=" + filepath.Join(dir, "aria2", "aria2.conf")}, filepath.Join(dir, "logs", "aria2.log"))
		if err != nil {
			return nil, nil, err
		}
		ready, stop := context.WithTimeout(ctx, 45*time.Second)
		defer stop()
		if err = waitPort(ready, p, c.Aria2Port); err != nil {
			p.stop()
			return nil, nil, err
		}
		return engine.NewAria2(c.Aria2Port, c.Secret), p.stop, nil
	}}
	amule := &lazyBackend{factory: func(ctx context.Context) (engine.Backend, func(), error) {
		if _, err := exec.LookPath(c.AMuledBinary); err != nil {
			return nil, nil, fmt.Errorf("ED2K requires the aMule engine; install the complete wirectl distribution: %w", err)
		}
		if err := availablePort(c.AMulePort); err != nil {
			return nil, nil, err
		}
		if err := BootstrapServers(ctx, dir, c.ServerListURLs, false); err != nil {
			return nil, nil, err
		}
		p, err := startOwnedProcess(c.AMuledBinary, []string{"--config-dir=" + filepath.Join(dir, "amule")}, filepath.Join(dir, "logs", "amuled.log"))
		if err != nil {
			return nil, nil, err
		}
		ready, stop := context.WithTimeout(ctx, 45*time.Second)
		defer stop()
		if err = waitPort(ready, p, c.AMulePort); err != nil {
			p.stop()
			return nil, nil, err
		}
		backend := engine.NewAMule(c.AMulecmdBinary, filepath.Join(dir, "amule"), c.Secret, c.AMulePort)
		if err = backend.Connect(ready); err != nil {
			backend.Close()
			p.stop()
			return nil, nil, err
		}
		return backend, p.stop, nil
	}}
	// Native engines and the fallback belong to the runtime context, never an
	// individual HTTP request's lifetime.
	aria.ctx = ctx
	amule.ctx = ctx
	vb := video.New(video.Options{Binary: c.YTDLPBinary, FFmpeg: c.FFmpegBinary, Deno: c.DenoBinary, StateDir: dir, Downloads: c.Downloads, Max: c.MaxDownloads, Limit: c.DownloadLimit})
	m.backends = []engine.Backend{builtin, aria, amule, vb}
	store, err := NewStore(dir, map[string]engine.Backend{"http": builtin, "aria2": aria, "amule": amule, "yt-dlp": vb})
	if err != nil {
		cancel()
		for _, b := range m.backends {
			b.Close()
		}
		return nil, err
	}
	store.preferHTTP = ariaAvailable != nil
	m.Store = store
	jobs, _ := store.Snapshot()
	for _, j := range jobs {
		if j.Status != "complete" && j.Status != "removed" && j.Status != "error" {
			if j.Engine == "aria2" {
				aria.required = true
			}
			if j.Engine == "amule" {
				amule.required = true
			}
		}
	}
	m.closeProxy, err = startAuthProxy(ctx, dir, c, store)
	if err != nil {
		cancel()
		for _, b := range m.backends {
			b.Close()
		}
		return nil, err
	}
	searches, err := NewSearchService(ctx, dir, func(ctx context.Context, req search.Request, emit search.Emit) error {
		b, err := amule.get()
		if err != nil {
			return err
		}
		return b.(*engine.AMule).Search(ctx, req, emit)
	})
	if err != nil {
		cancel()
		m.closeProxy()
		for _, b := range m.backends {
			b.Close()
		}
		return nil, err
	}
	m.searches = searches
	m.Handler = Handler(store, func() {}, searches)
	go func() {
		defer close(m.done)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				poll, done := context.WithTimeout(ctx, 20*time.Second)
				_ = store.Refresh(poll)
				done()
			}
		}
	}()
	return m, nil
}
func availablePort(port int) error {
	l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("engine port %d is in use: %w", port, err)
	}
	return l.Close()
}
func (m *Managed) Close() error {
	m.closeOnce.Do(func() {
		m.cancel()
		<-m.done
		m.searches.manager.Close()
		for _, b := range m.backends {
			if err := b.Close(); err != nil && m.closeErr == nil {
				m.closeErr = err
			}
		}
		if m.closeProxy != nil {
			m.closeProxy()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = m.Store.Refresh(ctx)
	})
	return m.closeErr
}

type lazyBackend struct {
	mu               sync.Mutex
	ctx              context.Context
	factory          func(context.Context) (engine.Backend, func(), error)
	backend          engine.Backend
	cleanup          func()
	required, closed bool
}

func (l *lazyBackend) get() (engine.Backend, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errors.New("download runtime is closed")
	}
	if l.backend == nil {
		b, cleanup, err := l.factory(l.ctx)
		if err != nil {
			return nil, err
		}
		l.backend = b
		l.cleanup = cleanup
	}
	return l.backend, nil
}
func (l *lazyBackend) Add(ctx context.Context, source string) (string, error) {
	return l.AddWithID(ctx, source, "")
}
func (l *lazyBackend) AddWithID(ctx context.Context, source, id string) (string, error) {
	b, err := l.get()
	if err != nil {
		return "", err
	}
	if x, ok := b.(interface {
		AddWithID(context.Context, string, string) (string, error)
	}); ok {
		return x.AddWithID(ctx, source, id)
	}
	return b.Add(ctx, source)
}
func (l *lazyBackend) List(ctx context.Context) ([]engine.Item, error) {
	l.mu.Lock()
	active := l.backend != nil || l.required
	l.mu.Unlock()
	if !active {
		return []engine.Item{}, nil
	}
	b, err := l.get()
	if err != nil {
		return nil, err
	}
	return b.List(ctx)
}
func (l *lazyBackend) Pause(ctx context.Context, id string) error {
	b, err := l.get()
	if err != nil {
		return err
	}
	return b.Pause(ctx, id)
}
func (l *lazyBackend) Resume(ctx context.Context, id string) error {
	b, err := l.get()
	if err != nil {
		return err
	}
	return b.Resume(ctx, id)
}
func (l *lazyBackend) Remove(ctx context.Context, id string) error {
	b, err := l.get()
	if err != nil {
		return err
	}
	return b.Remove(ctx, id)
}
func (l *lazyBackend) SaveSession(ctx context.Context) error {
	l.mu.Lock()
	b := l.backend
	l.mu.Unlock()
	if x, ok := b.(interface{ SaveSession(context.Context) error }); ok {
		return x.SaveSession(ctx)
	}
	return nil
}
func (l *lazyBackend) Forget(ctx context.Context, id string) error {
	l.mu.Lock()
	b := l.backend
	l.mu.Unlock()
	if x, ok := b.(interface {
		Forget(context.Context, string) error
	}); ok {
		return x.Forget(ctx, id)
	}
	return nil
}
func (l *lazyBackend) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.backend == nil {
		return nil
	}
	err := l.backend.Close()
	if l.cleanup != nil {
		l.cleanup()
	}
	return err
}
