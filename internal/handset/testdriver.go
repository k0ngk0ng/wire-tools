//go:build talk_test_audio

package handset

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"
)

// This source replaces native device access only in integration-test builds.
func (l *Listener) run(ctx context.Context) {
	path := os.Getenv("WIRE_TALK_TEST_HANDSET_EVENTS")
	if path == "" {
		l.update(false, "test handset not configured")
		<-ctx.Done()
		return
	}
	l.update(true, "")
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	offset := 0
	if b, err := os.ReadFile(path); err == nil {
		offset = len(b)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if offset > len(data) {
				offset = 0
			}
			rest := string(data[offset:])
			for {
				i := strings.IndexByte(rest, '\n')
				if i < 0 {
					break
				}
				var event struct {
					Page  uint32 `json:"usage_page"`
					Usage uint32 `json:"usage"`
					Value int    `json:"value"`
				}
				if json.Unmarshal([]byte(rest[:i]), &event) == nil && l.enabled() {
					l.input(event.Page, event.Usage, event.Value)
				}
				offset += i + 1
				rest = rest[i+1:]
			}
		}
	}
}
