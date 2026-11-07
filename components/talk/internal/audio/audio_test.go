package audio

import (
	"bytes"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

func TestNativeNullDeviceRunsDuplexCallbacks(t *testing.T) {
	var captured, played, invalid atomic.Int32
	// malgo v0.11.24 omits ma_backend_custom from its Go enum; the pinned
	// miniaudio.h defines ma_backend_null as 14 (BackendNull incorrectly is 13).
	a, err := openWithBackends([]malgo.Backend{malgo.Backend(14)}, "", "", true, func(p []byte) {
		if len(p) != room.FrameBytes {
			invalid.Add(1)
		}
		captured.Add(1)
	}, func(p []byte) { clear(p); played.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && (captured.Load() < 3 || played.Load() < 3) {
		time.Sleep(10 * time.Millisecond)
	}
	a.Close()
	if captured.Load() < 3 || played.Load() < 3 || invalid.Load() != 0 {
		t.Fatalf("capture=%d playback=%d invalid=%d", captured.Load(), played.Load(), invalid.Load())
	}
	after := captured.Load()
	time.Sleep(40 * time.Millisecond)
	if captured.Load() != after {
		t.Fatal("capture continued after close")
	}
}
func TestMutedPlaybackConsumesAudioAndResumesAtCurrentPosition(t *testing.T) {
	frame := []byte{1, 2, 3, 4, 5, 6}
	out := []byte{99, 99, 99, 99}
	n := copyPlayback(out, frame, true)
	if n != 4 || !bytes.Equal(out, []byte{0, 0, 0, 0}) {
		t.Fatalf("muted playback: %v (%d consumed)", out, n)
	}
	n = copyPlayback(out[:2], frame[n:], false)
	if n != 2 || !bytes.Equal(out[:2], []byte{5, 6}) {
		t.Fatalf("resumed playback replayed old audio: %v", out)
	}
}

func TestLevelMeter(t *testing.T) {
	silence := Measure(make([]byte, 640))
	if silence.RMS != 0 || silence.Peak != 0 || silence.Clipped {
		t.Fatal("silence meter")
	}
	half := bytes.Repeat([]byte{0, 64}, 320)
	l := Measure(half)
	if l.RMS != 0.5 || l.Peak != 0.5 || l.Clipped {
		t.Fatalf("half scale: %+v", l)
	}
	clip := Measure([]byte{0, 128})
	if !clip.Clipped || clip.Peak != 1 {
		t.Fatalf("clip: %+v", clip)
	}
}
