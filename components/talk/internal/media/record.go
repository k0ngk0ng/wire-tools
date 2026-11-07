package media

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

type RecordStatus struct {
	State string `json:"state"`
	File  string `json:"file,omitempty"`
	Peer  string `json:"peer,omitempty"`
	Bytes uint64 `json:"bytes"`
	Error string `json:"error,omitempty"`
}
type recording struct {
	mu     sync.Mutex
	status RecordStatus
	frames chan []byte
	done   chan struct{}
	closed bool // protected by Session.mu
}

func wavHeader(size uint32) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], size+36)
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], room.SampleRate)
	binary.LittleEndian.PutUint32(b[28:], room.SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], size)
	return b
}
func openRecording(path, peer string) (*recording, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = f.Write(wavHeader(0)); err != nil {
		f.Close()
		return nil, err
	}
	r := &recording{status: RecordStatus{State: "recording", File: path, Peer: peer}, frames: make(chan []byte, 256), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		var size uint32
		var writeErr error
		for pcm := range r.frames {
			if writeErr != nil {
				continue
			}
			if uint64(size)+uint64(len(pcm)) > uint64(^uint32(0))-36 {
				writeErr = fmt.Errorf("WAV size limit reached; start a new recording")
			} else {
				var n int
				n, writeErr = f.Write(pcm)
				size += uint32(n)
				if writeErr == nil && n != len(pcm) {
					writeErr = io.ErrShortWrite
				}
			}
			r.mu.Lock()
			r.status.Bytes = uint64(size)
			if writeErr != nil {
				r.status.Error = writeErr.Error()
				r.status.State = "error"
			}
			r.mu.Unlock()
		}
		// Always finalize the header, including when the disk filled mid-recording.
		_, err := f.WriteAt(wavHeader(size), 0)
		if err == nil {
			err = f.Sync()
		}
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		r.mu.Lock()
		if err != nil && r.status.Error == "" {
			r.status.Error = err.Error()
		}
		if r.status.Error != "" {
			r.status.State = "error"
		} else {
			r.status.State = "stopped"
		}
		r.mu.Unlock()
	}()
	return r, nil
}
func (r *recording) snapshot() RecordStatus { r.mu.Lock(); defer r.mu.Unlock(); return r.status }
func (r *recording) enqueue(pcm []byte) {
	if r.closed {
		return
	}
	if r.snapshot().State == "error" {
		close(r.frames)
		r.closed = true
		return
	}
	select {
	case r.frames <- append([]byte(nil), pcm...):
	default:
		r.mu.Lock()
		r.status.State = "error"
		r.status.Error = "recording stopped: disk writer could not keep up"
		r.mu.Unlock()
		close(r.frames)
		r.closed = true
	}
}
func (r *recording) stop() error {
	if !r.closed {
		close(r.frames)
		r.closed = true
	}
	<-r.done
	if s := r.snapshot(); s.Error != "" {
		return fmt.Errorf("recording: %s", s.Error)
	}
	return nil
}
