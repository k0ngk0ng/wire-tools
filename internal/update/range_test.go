package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Failure scenarios precede implementation: missing/oversize metadata size,
// wrong probe/range total, invalid/duplicate/out-of-bounds ranges, short/long
// bodies, unexpected 200, changed bytes, unsafe redirect, cancellation, signed
// URL disclosure. A 200 probe is the explicit bounded no-Range fallback.
func largeArchiveFixture(t *testing.T) []byte {
	t.Helper()
	payload := make([]byte, 18<<20)
	if _, err := rand.New(rand.NewSource(19)).Read(payload); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"bin/wirectl" + exeSuffix(): []byte("large range update"), "libexec/wirectl-download/payload": payload}
	hashes := map[string]string{}
	for name, data := range files {
		hashes[name] = digest(data)
	}
	meta, err := json.Marshal(metadata{Name: "wirectl", Version: "9.8.6", Platform: runtime.GOOS + "-" + runtime.GOARCH, Files: hashes})
	if err != nil {
		t.Fatal(err)
	}
	files["wirectl-package.json"] = meta
	var compressed bytes.Buffer
	if runtime.GOOS == "windows" {
		writer := zip.NewWriter(&compressed)
		for name, data := range files {
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			header.SetMode(0755)
			out, err := writer.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = out.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&compressed)
		writer := tar.NewWriter(gz)
		for name, data := range files {
			if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err = writer.Write(data); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return compressed.Bytes()
}

type rangeFixture struct {
	base                        *onlineFixture
	server                      *httptest.Server
	mode                        string
	mu                          sync.Mutex
	apiCalls, active, maxActive int
	ranges                      int
	connections                 map[string]bool
	protocols                   map[int]bool
	expiredReady                chan struct{}
	expiredSeen                 int
}

func newRangeFixture(t *testing.T, data []byte, mode string) *rangeFixture {
	t.Helper()
	f := &rangeFixture{base: newOnlineFixture(t), mode: mode, connections: map[string]bool{}, protocols: map[int]bool{}}
	f.base.archive = data
	f.base.sums = []byte(digest(data) + "  " + f.base.name + "\n")
	f.base.assets[0]["digest"] = "sha256:" + digest(data)
	f.base.assets[0]["size"] = len(data)
	f.base.assets[1]["digest"] = "sha256:" + digest(f.base.sums)
	f.base.assets[1]["size"] = len(f.base.sums)
	if mode == "missing-size" {
		delete(f.base.assets[0], "size")
	}
	if mode == "oversize" {
		f.base.assets[0]["size"] = archiveLimit + 1
	}
	f.expiredReady = make(chan struct{})
	f.server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.server.EnableHTTP2 = true
	f.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	f.server.StartTLS()
	transport := f.server.Client().Transport.(*http.Transport).Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = "example.com" // SAN in httptest's trusted certificate.
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("fixture must retain certificate verification")
	}
	transport.ForceAttemptHTTP2 = true
	address := f.server.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); f.server.Close() })
	return f
}
func (f *rangeFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/repos/"+repository+"/releases/latest" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v9.8.6", "assets": f.base.assets})
		return
	}
	if r.URL.Path == "/repos/"+repository+"/releases/assets/101" {
		f.mu.Lock()
		f.apiCalls++
		generation := f.apiCalls
		f.mu.Unlock()
		location := "https://release-assets.githubusercontent.com/fixture/large?token=must-not-leak"
		if strings.HasPrefix(f.mode, "expired-") {
			location += fmt.Sprintf("&generation=%d", generation)
		}
		if generation > 1 && r.Header.Get("Cache-Control") != "no-cache" {
			http.Error(w, "refresh requires cache revalidation", 400)
			return
		}
		if f.mode == "unsafe-redirect" {
			location = "https://untrusted.invalid/large?token=must-not-leak"
		}
		http.Redirect(w, r, location, 302)
		return
	}
	if r.URL.Path == "/repos/"+repository+"/releases/assets/102" {
		w.Header().Set("Content-Length", fmt.Sprint(len(f.base.sums)))
		w.Write(f.base.sums)
		return
	}
	if r.URL.Path != "/fixture/large" {
		http.Error(w, "fixture route missing", 404)
		return
	}
	requested := r.Header.Get("Range")
	if f.mode == "fallback" {
		w.Header().Set("Content-Length", fmt.Sprint(len(f.base.archive)))
		w.Write(f.base.archive)
		return
	}
	if requested == "" {
		http.Error(w, "archive requires a range probe", 400)
		return
	}
	total := int64(len(f.base.archive))
	if requested == "bytes=0-0" {
		reported := total
		if f.mode == "probe-total" || f.mode == "expired-refresh-total" && r.URL.Query().Get("generation") != "1" {
			reported++
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", reported))
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(206)
		w.Write(f.base.archive[:1])
		return
	}
	var start, end int64
	if _, err := fmt.Sscanf(requested, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end < start || end >= total {
		http.Error(w, "invalid outbound range", 416)
		return
	}
	f.mu.Lock()
	f.active++
	f.ranges++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.connections[r.RemoteAddr] = true
	f.protocols[r.ProtoMajor] = true
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.mu.Unlock() }()
	if strings.HasPrefix(f.mode, "expired-") && (r.URL.Query().Get("generation") == "1" || f.mode == "expired-always") {
		if f.mode != "expired-always" {
			f.mu.Lock()
			f.expiredSeen++
			if f.expiredSeen == 8 {
				close(f.expiredReady)
			}
			f.mu.Unlock()
			select {
			case <-f.expiredReady:
			case <-r.Context().Done():
				return
			}
		}
		status := 403
		if f.mode == "expired-401" {
			status = 401
		}
		if f.mode == "expired-618" {
			status = 618
		}
		w.WriteHeader(status)
		return
	}
	if f.mode == "cancel" {
		<-r.Context().Done()
		return
	}
	time.Sleep(20 * time.Millisecond)
	reportedStart, reportedEnd, reportedTotal := start, end, total
	data := f.base.archive[start : end+1]
	switch f.mode {
	case "range-total":
		reportedTotal++
	case "duplicate-range":
		if start > 0 {
			reportedStart = 0
			reportedEnd = end - start
		}
	case "out-of-bounds":
		reportedEnd = total
	case "truncated":
		data = data[:len(data)-1]
	case "extra-byte":
		data = append(append([]byte{}, data...), 0)
	case "wrong-status":
		w.WriteHeader(200)
		w.Write([]byte("range ignored"))
		return
	case "duplicate-bytes":
		if start > 0 {
			data = f.base.archive[:len(data)]
		}
	}
	header := fmt.Sprintf("bytes %d-%d/%d", reportedStart, reportedEnd, reportedTotal)
	if f.mode == "invalid-range" {
		header = "bytes nonsense"
	}
	w.Header().Set("Content-Range", header)
	w.Header().Set("Content-Length", fmt.Sprint(len(data)))
	w.WriteHeader(206)
	w.Write(data)
}
func TestLargeOnlineUpdateUsesBoundedIndependentTCPRanges(t *testing.T) {
	f := newRangeFixture(t, largeArchiveFixture(t), "")
	if _, err := Run(context.Background(), Options{Executable: f.base.target}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apiCalls != 1 {
		t.Fatalf("asset REST API called %d times", f.apiCalls)
	}
	if f.ranges < 2 || len(f.connections) < 2 || f.maxActive < 2 || f.maxActive > 8 {
		t.Fatalf("range TCP/concurrency: ranges=%d connections=%d active=%d", f.ranges, len(f.connections), f.maxActive)
	}
	if len(f.protocols) != 1 || !f.protocols[1] {
		t.Fatalf("asset requests did not use HTTP/1.1: %v", f.protocols)
	}
	t.Logf("REST probes=%d ranges=%d distinct TCP=%d max concurrent=%d protocol=HTTP/1.1", f.apiCalls, f.ranges, len(f.connections), f.maxActive)
}
func TestLargeOnlineUpdateHasBoundedNoRangeFallback(t *testing.T) {
	f := newRangeFixture(t, largeArchiveFixture(t), "fallback")
	if _, err := Run(context.Background(), Options{Executable: f.base.target}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.apiCalls != 1 || f.ranges != 0 {
		t.Fatalf("fallback made extra calls: api=%d ranges=%d", f.apiCalls, f.ranges)
	}
}
func TestLargeOnlineUpdateRejectsBadRangesWithoutInstalling(t *testing.T) {
	data := largeArchiveFixture(t)
	for _, mode := range []string{"missing-size", "oversize", "probe-total", "range-total", "duplicate-range", "out-of-bounds", "truncated", "extra-byte", "wrong-status", "duplicate-bytes", "invalid-range", "unsafe-redirect"} {
		t.Run(mode, func(t *testing.T) {
			f := newRangeFixture(t, data, mode)
			if _, err := Run(context.Background(), Options{Executable: f.base.target}); err == nil {
				t.Fatal("invalid range update accepted")
			} else if strings.Contains(err.Error(), "must-not-leak") {
				t.Fatal("signed query leaked")
			}
			f.base.unchanged(t)
		})
	}
}
func TestLargeOnlineUpdateHonorsCallerCancellation(t *testing.T) {
	f := newRangeFixture(t, largeArchiveFixture(t), "cancel")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := Run(ctx, Options{Executable: f.base.target})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("caller deadline not preserved: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("cancelled update did not finish promptly")
	}
	f.base.unchanged(t)
}
func TestAssetRequestErrorsNeverExposeSignedQueries(t *testing.T) {
	signed := "https://release-assets.githubusercontent.com/asset?token=must-not-leak"
	previous := http.DefaultTransport
	http.DefaultTransport = updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "api.github.com" {
			return fixtureResponse(r, 302, nil, signed), nil
		}
		return nil, &url.Error{Op: "Get", URL: signed, Err: io.ErrUnexpectedEOF}
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	_, err := fetchAsset(context.Background(), "https://api.github.com/repos/"+repository+"/releases/assets/101", 100)
	if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("transport cause lost: %v", err)
	}
	if strings.Contains(err.Error(), "must-not-leak") || strings.Contains(err.Error(), "token=") {
		t.Fatal("signed asset query leaked in error")
	}
}

func TestLargeOnlineUpdateRefreshesExpiredSignedURLOnceForConcurrentFailures(t *testing.T) {
	data := largeArchiveFixture(t)
	for _, mode := range []string{"expired-401", "expired-403", "expired-618"} {
		t.Run(mode, func(t *testing.T) {
			f := newRangeFixture(t, data, mode)
			if _, err := Run(context.Background(), Options{Executable: f.base.target}); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.apiCalls != 2 {
				t.Fatalf("concurrent stale links caused %d REST probes, want 2 total", f.apiCalls)
			}
			t.Logf("8 simultaneous stale responses coalesced: total REST probes=%d", f.apiCalls)
		})
	}
}
func TestExpiredSignedURLRefreshIsBoundedAndValidated(t *testing.T) {
	data := largeArchiveFixture(t)
	for _, mode := range []string{"expired-always", "expired-refresh-total"} {
		t.Run(mode, func(t *testing.T) {
			f := newRangeFixture(t, data, mode)
			if _, err := Run(context.Background(), Options{Executable: f.base.target}); err == nil {
				t.Fatal("invalid signed-link refresh accepted")
			} else if strings.Contains(err.Error(), "must-not-leak") {
				t.Fatal("refresh leaked signed query")
			}
			f.base.unchanged(t)
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.apiCalls < 2 || f.apiCalls > 5 {
				t.Fatalf("refresh attempts not bounded: %d", f.apiCalls)
			}
			if mode == "expired-refresh-total" && f.apiCalls != 2 {
				t.Fatalf("invalid refresh was repeated: %d", f.apiCalls)
			}
		})
	}
}
func TestContentRangeHeaderCannotBeRepeated(t *testing.T) {
	response := &http.Response{StatusCode: 206, Header: make(http.Header)}
	response.Header.Add("Content-Range", "bytes 0-0/10")
	response.Header.Add("Content-Range", "bytes 1-1/10")
	if err := expectedRange(response, 0, 0, 10); err == nil {
		t.Fatal("duplicate Content-Range headers accepted")
	}
}

func TestAssetResponseStatusNeverExposesSignedQuery(t *testing.T) {
	previous := http.DefaultTransport
	http.DefaultTransport = updateRoundTrip(func(r *http.Request) (*http.Response, error) {
		response := fixtureResponse(r, 403, nil, "")
		response.Status = "403 token=must-not-leak"
		return response, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	_, err := fetchAsset(context.Background(), "https://api.github.com/repos/"+repository+"/releases/assets/101", 100)
	if err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatal("asset status leaked signed data")
	}
}
