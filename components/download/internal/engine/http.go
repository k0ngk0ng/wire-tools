package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/auth"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// HTTP is the built-in HTTP(S) backend for installations without aria2. Its
// private partial files and task journal allow interrupted transfers to resume.
// Torrent metadata and magnet/ED2K protocols remain native-engine operations.
type HTTP struct {
	mu           sync.Mutex
	ctx          context.Context
	cancel       context.CancelFunc
	dir, journal string
	stateDir     string
	tasks        map[string]*httpTask
	wg           sync.WaitGroup
	closed       bool
}
type httpTask struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Item        Item   `json:"item"`
	cancel      context.CancelFunc
	running     bool
	done        chan struct{}
}

func NewHTTP(ctx context.Context, dir, state string) (*HTTP, error) {
	ctx, cancel := context.WithCancel(ctx)
	h := &HTTP{ctx: ctx, cancel: cancel, dir: dir, journal: filepath.Join(state, "http-jobs.json"), stateDir: state, tasks: map[string]*httpTask{}}
	if err := os.MkdirAll(dir, 0700); err != nil {
		cancel()
		return nil, err
	}
	b, err := os.ReadFile(h.journal)
	if err == nil {
		err = json.Unmarshal(b, &h.tasks)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cancel()
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, t := range h.tasks {
		if t == nil || t.Item.ID != id || filepath.Dir(t.Destination) != dir {
			cancel()
			return nil, errors.New("invalid HTTP task journal")
		}
		if t.Item.Status != "complete" && t.Item.Status != "removed" && t.Item.Status != "paused" && t.Item.Status != "error" {
			h.startLocked(t)
		}
	}
	return h, nil
}
func (h *HTTP) saveLocked() error {
	data, err := json.Marshal(h.tasks)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(h.journal), ".http-jobs-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, h.journal)
}
func (h *HTTP) Add(ctx context.Context, source string) (string, error) {
	return h.AddWithID(ctx, source, "")
}
func (h *HTTP) AddWithID(_ context.Context, source, id string) (string, error) {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("BitTorrent requires the aria2 engine; install the complete wirectl distribution")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", errors.New("download runtime is closed")
	}
	if id == "" {
		b := make([]byte, 8)
		if _, err = rand.Read(b); err != nil {
			return "", err
		}
		id = hex.EncodeToString(b)
	}
	if _, exists := h.tasks[id]; exists {
		return id, nil
	}
	name := path.Base(u.Path)
	if decoded, e := url.PathUnescape(name); e == nil {
		name = decoded
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "_"))
	if name == "." || name == ".." || name == "/" || name == "" || strings.ContainsAny(name, "\x00\r\n") {
		name = "download-" + id
	}
	dest := filepath.Join(h.dir, name)
	occupied := false
	if _, err = os.Lstat(dest); err == nil {
		occupied = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, task := range h.tasks {
		if task.Destination == dest {
			occupied = true
		}
	}
	if occupied {
		dest = filepath.Join(h.dir, id+"-"+name)
	}
	t := &httpTask{Source: source, Destination: dest, Item: Item{ID: id, Name: filepath.Base(dest), Status: "waiting"}}
	h.tasks[id] = t
	if err = h.saveLocked(); err != nil {
		delete(h.tasks, id)
		return "", err
	}
	h.startLocked(t)
	return id, nil
}
func (h *HTTP) startLocked(t *httpTask) {
	if t.running || h.closed {
		return
	}
	ctx, cancel := context.WithCancel(h.ctx)
	t.cancel = cancel
	t.running = true
	t.done = make(chan struct{})
	t.Item.Status = "active"
	t.Item.Error = ""
	h.wg.Add(1)
	go func() { defer h.wg.Done(); h.transfer(ctx, t) }()
}
func (h *HTTP) transfer(ctx context.Context, t *httpTask) {
	partial := filepath.Join(h.dir, ".wirectl-"+t.Item.ID+".part")
	file, err := os.OpenFile(partial, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		h.finish(t, err)
		return
	}
	offset, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		file.Close()
		h.finish(t, err)
		return
	}
	req, err := http.NewRequestWithContext(ctx, "GET", t.Source, nil)
	if err != nil {
		file.Close()
		h.finish(t, err)
		return
	}
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 10 {
			return errors.New("too many download redirects")
		}
		if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
			return errors.New("unsupported download redirect")
		}
		if r.URL.Scheme == "http" && via[len(via)-1].URL.Scheme == "https" {
			return errors.New("HTTPS download redirected to HTTP")
		}
		return nil
	}}
	session, err := auth.Load(h.stateDir, t.Source)
	if err != nil {
		file.Close()
		h.finish(t, err)
		return
	}
	if session != nil {
		client.Jar, err = session.Jar()
		if err != nil {
			file.Close()
			h.finish(t, err)
			return
		}
		req.Header.Set("User-Agent", session.UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		file.Close()
		h.finish(t, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		file.Close()
		h.finish(t, fmt.Errorf("download HTTP %d", resp.StatusCode))
		return
	}
	if resp.StatusCode == http.StatusPartialContent {
		prefix := "bytes " + strconv.FormatInt(offset, 10) + "-"
		if !strings.HasPrefix(resp.Header.Get("Content-Range"), prefix) {
			file.Close()
			h.finish(t, errors.New("invalid resumed Content-Range"))
			return
		}
	} else if offset > 0 {
		if err = file.Truncate(0); err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
		if err != nil {
			file.Close()
			h.finish(t, err)
			return
		}
		offset = 0
	}
	h.mu.Lock()
	t.Item.Completed = offset
	if resp.ContentLength >= 0 {
		t.Item.Total = offset + resp.ContentLength
	}
	h.mu.Unlock()
	buf := make([]byte, 64<<10)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written, writeErr := file.Write(buf[:n])
			h.mu.Lock()
			t.Item.Completed += int64(written)
			if t.Item.Total > 0 {
				t.Item.Progress = 100 * float64(t.Item.Completed) / float64(t.Item.Total)
			}
			h.mu.Unlock()
			if writeErr != nil {
				err = writeErr
				break
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = readErr
			}
			break
		}
	}
	if err == nil {
		err = file.Sync()
	}
	if e := file.Close(); err == nil {
		err = e
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		// A hard link publishes atomically without overwriting a file created by
		// another application while this transfer was running.
		err = os.Link(partial, t.Destination)
		if err == nil {
			err = os.Remove(partial)
		}
	}
	h.finish(t, err)
}
func (h *HTTP) finish(t *httpTask, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	t.running = false
	if t.done != nil {
		close(t.done)
		t.done = nil
	}
	t.cancel = nil
	if t.Item.Status == "paused" || t.Item.Status == "removed" {
		_ = h.saveLocked()
		return
	}
	if err != nil {
		if h.closed || h.ctx.Err() != nil {
			t.Item.Status = "waiting"
		} else {
			t.Item.Status = "error"
			t.Item.Error = err.Error()
		}
	} else {
		t.Item.Status = "complete"
		t.Item.Progress = 100
		if t.Item.Total == 0 {
			t.Item.Total = t.Item.Completed
		}
	}
	if saveErr := h.saveLocked(); saveErr != nil {
		t.Item.Status = "error"
		t.Item.Error = "save HTTP task state: " + saveErr.Error()
	}
}
func (h *HTTP) List(context.Context) ([]Item, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	items := []Item{}
	for _, t := range h.tasks {
		items = append(items, t.Item)
	}
	return items, nil
}
func (h *HTTP) action(id, action string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	t := h.tasks[id]
	if t == nil {
		return os.ErrNotExist
	}
	switch action {
	case "pause":
		if t.Item.Status != "complete" {
			t.Item.Status = "paused"
			if t.cancel != nil {
				t.cancel()
			}
		}
	case "resume":
		if t.Item.Status != "complete" && t.Item.Status != "removed" {
			if t.running {
				return errors.New("download is still pausing; retry resume")
			}
			h.startLocked(t)
		}
	case "remove":
		t.Item.Status = "removed"
		if t.cancel != nil {
			t.cancel()
		}
	}
	return h.saveLocked()
}
func (h *HTTP) Pause(_ context.Context, id string) error { return h.action(id, "pause") }
func (h *HTTP) Resume(ctx context.Context, id string) error {
	h.mu.Lock()
	t := h.tasks[id]
	if t == nil {
		h.mu.Unlock()
		return os.ErrNotExist
	}
	done := t.done
	pausing := t.running && t.Item.Status == "paused"
	h.mu.Unlock()
	if pausing {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	return h.action(id, "resume")
}
func (h *HTTP) Remove(_ context.Context, id string) error { return h.action(id, "remove") }
func (h *HTTP) Close() error {
	h.mu.Lock()
	h.closed = true
	h.cancel()
	h.mu.Unlock()
	h.wg.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.saveLocked()
}
