package command

import (
	"errors"
	"time"
)

var errAudioShutdownTimeout = errors.New("audio driver did not close within 3 seconds; exiting to release devices and the session lock")

// A native backend may block forever in device uninitialization. We must exit
// the process on this error (including the login-service worker), never retry
// a new session with a native device that still belongs to the old one.
func closeAudioWithin(closeDevice func(), timeout time.Duration) error {
	done := make(chan struct{})
	go func() { defer close(done); closeDevice() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return errAudioShutdownTimeout
	}
}
