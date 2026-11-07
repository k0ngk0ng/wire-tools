package command

import (
	"errors"
	"testing"
	"time"
)

func TestAudioShutdownDeadline(t *testing.T) {
	if err := closeAudioWithin(func() {}, time.Second); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	if err := closeAudioWithin(func() { <-release }, 10*time.Millisecond); !errors.Is(err, errAudioShutdownTimeout) {
		t.Fatalf("native shutdown not bounded: %v", err)
	}
}
