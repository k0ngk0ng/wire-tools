package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const archiveTimeout = 20 * time.Minute
const rangeThreshold int64 = 8 << 20
const rangeChunk int64 = 2 << 20
const rangeWorkers = 8
const signedLinkRefreshes = 4

var errUntrustedUpdateRedirect = errors.New("untrusted update redirect")

var contentRange = regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+)$`)

// Preserve cancellation/error identity without rendering signed CDN queries.
type assetTransferError struct{ cause error }

func (e *assetTransferError) Error() string {
	switch {
	case errors.Is(e.cause, errUntrustedUpdateRedirect):
		return "untrusted update redirect"
	case errors.Is(e.cause, context.Canceled):
		return "release asset download canceled"
	case errors.Is(e.cause, context.DeadlineExceeded):
		return "release asset download timed out"
	case errors.Is(e.cause, io.EOF), errors.Is(e.cause, io.ErrUnexpectedEOF):
		return "release asset transfer ended early"
	default:
		return "release asset request failed"
	}
}
func (e *assetTransferError) Unwrap() error { return e.cause }
func safeAssetError(err error) error {
	if err == nil {
		return nil
	}
	return &assetTransferError{cause: err}
}
func updateRedirect(r *http.Request, via []*http.Request) error {
	if len(via) > 5 || !allowed(r.URL) {
		return errUntrustedUpdateRedirect
	}
	return nil
}
func newAssetClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport
	if base, ok := transport.(*http.Transport); ok {
		cloned := base.Clone()
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		cloned.Protocols = protocols
		cloned.ForceAttemptHTTP2 = false
		cloned.TLSNextProto = nil
		if cloned.TLSClientConfig != nil {
			cloned.TLSClientConfig.NextProtos = []string{"http/1.1"}
		}
		cloned.DisableKeepAlives = true
		cloned.MaxConnsPerHost = rangeWorkers
		transport = cloned
	}
	return &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: updateRedirect}
}
func closeAssetClient(client *http.Client) {
	if transport, ok := client.Transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}
func assetResponse(ctx context.Context, client *http.Client, address, requested string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, safeAssetError(err)
	}
	if !allowed(request.URL) {
		return nil, errors.New("untrusted update URL")
	}
	request.Header.Set("User-Agent", "wirectl-updater")
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("Accept-Encoding", "identity")
	if request.URL.Hostname() == "api.github.com" {
		request.Header.Set("Cache-Control", "no-cache")
	}
	if requested != "" {
		request.Header.Set("Range", requested)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, safeAssetError(err)
	}
	return response, nil
}
func expectedBody(response *http.Response, size int64) ([]byte, error) {
	defer response.Body.Close()
	if encoding := response.Header.Get("Content-Encoding"); encoding != "" && encoding != "identity" {
		return nil, errors.New("unexpected release asset content encoding")
	}
	if response.ContentLength >= 0 && response.ContentLength != size {
		return nil, errors.New("release asset Content-Length mismatch")
	}
	data, err := bounded(response.Body, size)
	if err != nil {
		return nil, safeAssetError(err)
	}
	if int64(len(data)) != size {
		return nil, errors.New("release asset body length mismatch")
	}
	return data, nil
}
func expectedRange(response *http.Response, start, end, total int64) error {
	if response.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("release asset range response HTTP %d; expected 206", response.StatusCode)
	}
	if len(response.Header.Values("Content-Range")) != 1 {
		return errors.New("invalid or duplicate release asset Content-Range")
	}
	values := contentRange.FindStringSubmatch(response.Header.Get("Content-Range"))
	if len(values) != 4 {
		return errors.New("invalid release asset Content-Range")
	}
	expected := []int64{start, end, total}
	for i, value := range values[1:] {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil || parsed != expected[i] {
			return errors.New("release asset Content-Range mismatch")
		}
	}
	if start < 0 || end < start || end >= total {
		return errors.New("release asset range is out of bounds")
	}
	return nil
}
func downloadArchive(ctx context.Context, item asset) ([]byte, error) {
	if item.Size <= 0 || item.Size > archiveLimit {
		return nil, errors.New("release archive metadata size is missing or exceeds its limit")
	}
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	client := newAssetClient(archiveTimeout)
	defer closeAssetClient(client)
	if item.Size <= rangeThreshold {
		response, err := assetResponse(ctx, client, item.APIURL, "")
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("release asset HTTP %d", response.StatusCode)
		}
		return expectedBody(response, item.Size)
	}
	probe, err := assetResponse(ctx, client, item.APIURL, "bytes=0-0")
	if err != nil {
		return nil, err
	}
	if probe.StatusCode == http.StatusOK {
		return expectedBody(probe, item.Size)
	}
	address, err := trustedRangeURL(probe, item.Size)
	if err != nil {
		return nil, err
	}
	source := &archiveSource{client: client, item: item, address: address}
	data := make([]byte, item.Size)
	type span struct{ start, end int64 }
	jobs := make(chan span, (item.Size+rangeChunk-1)/rangeChunk)
	for start := int64(0); start < item.Size; start += rangeChunk {
		end := min(start+rangeChunk, item.Size) - 1
		jobs <- span{start, end}
	}
	close(jobs)
	failed := make(chan error, 1)
	var workers sync.WaitGroup
	for worker := 0; worker < rangeWorkers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				chunk, err := source.part(ctx, job.start, job.end)
				if err == nil {
					copy(data[job.start:job.end+1], chunk)
				}
				if err != nil {
					select {
					case failed <- err:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
	workers.Wait()
	select {
	case err := <-failed:
		return nil, err
	default:
	}
	if ctx.Err() != nil {
		return nil, safeAssetError(ctx.Err())
	}
	return data, nil
}

func trustedRangeURL(probe *http.Response, size int64) (string, error) {
	if err := expectedRange(probe, 0, 0, size); err != nil {
		probe.Body.Close()
		return "", err
	}
	if _, err := expectedBody(probe, 1); err != nil {
		return "", err
	}
	if probe.Request == nil || probe.Request.URL == nil || !allowed(probe.Request.URL) {
		return "", errors.New("missing trusted release asset redirect")
	}
	switch probe.Request.URL.Hostname() {
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
	default:
		return "", errors.New("range download requires the official release asset CDN")
	}
	return probe.Request.URL.String(), nil
}

// URL capabilities stay in memory. The mutex coalesces simultaneous expired
// responses into one fresh probe of the same immutable official asset.
type archiveSource struct {
	mu                    sync.Mutex
	client                *http.Client
	item                  asset
	address               string
	generation, refreshes int
	failed                error
}

func (s *archiveSource) current() (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.address, s.generation
}
func (s *archiveSource) refresh(ctx context.Context, stale int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return s.failed
	}
	if s.generation != stale {
		return nil
	}
	if ctx.Err() != nil {
		return safeAssetError(ctx.Err())
	}
	if s.refreshes >= signedLinkRefreshes {
		s.failed = errors.New("release asset signed link refresh limit exceeded")
		return s.failed
	}
	s.refreshes++
	probe, err := assetResponse(ctx, s.client, s.item.APIURL, "bytes=0-0")
	var address string
	if err == nil {
		address, err = trustedRangeURL(probe, s.item.Size)
	}
	if err != nil {
		s.failed = err
		return err
	}
	s.address = address
	s.generation++
	return nil
}
func (s *archiveSource) part(ctx context.Context, start, end int64) ([]byte, error) {
	for attempt := 0; attempt <= signedLinkRefreshes; attempt++ {
		address, generation := s.current()
		response, err := assetResponse(ctx, s.client, address, fmt.Sprintf("bytes=%d-%d", start, end))
		if err != nil {
			return nil, err
		}
		switch response.StatusCode {
		case 401, 403, 618:
			response.Body.Close()
			if err = s.refresh(ctx, generation); err != nil {
				return nil, err
			}
			continue
		}
		if err = expectedRange(response, start, end, s.item.Size); err != nil {
			response.Body.Close()
			return nil, err
		}
		return expectedBody(response, end-start+1)
	}
	return nil, errors.New("release asset signed link retry limit exceeded")
}
