package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

func TestDecodersResampleStereoAndFinish(t *testing.T) {
	for _, ext := range []string{"wav", "mp3", "flac"} {
		t.Run(ext, func(t *testing.T) {
			p, err := openPlayer("testdata/tone."+ext, "replace", false)
			if err != nil {
				t.Fatal(err)
			}
			defer p.stop()
			frames, nonzero := 0, false
			for {
				select {
				case f := <-p.frames:
					if f.err != nil {
						t.Fatal(f.err)
					}
					if f.end {
						if frames < 9 || frames > 16 || !nonzero {
							t.Fatalf("frames=%d nonzero=%v", frames, nonzero)
						}
						return
					}
					if len(f.pcm) != room.FrameBytes {
						t.Fatal("wrong frame size")
					}
					frames++
					if !bytes.Equal(f.pcm, make([]byte, room.FrameBytes)) {
						nonzero = true
					}
				case <-time.After(3 * time.Second):
					t.Fatal("decoder did not finish")
				}
			}
		})
	}
}
func TestLoopAndCancellation(t *testing.T) {
	p, err := openPlayer("testdata/tone.wav", "mix", true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 25; i++ {
		select {
		case f := <-p.frames:
			if f.end || f.err != nil {
				t.Fatal("loop ended")
			}
		case <-time.After(time.Second):
			t.Fatal("loop stalled")
		}
	}
	p.stop() // Must unblock a decoder whose output channel has filled.
}
func TestRecordingFinalizesWAVAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "received.wav")
	r, err := openRecording(path, "all")
	if err != nil {
		t.Fatal(err)
	}
	pcm := bytes.Repeat([]byte{1, 2}, room.FrameSamples)
	r.enqueue(pcm)
	r.enqueue(pcm)
	if err = r.stop(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:4]) != "RIFF" || binary.LittleEndian.Uint32(b[40:]) != 2*room.FrameBytes || !bytes.Equal(b[44:], append(append([]byte{}, pcm...), pcm...)) {
		t.Fatal("invalid recording")
	}
	if _, err = openRecording(path, "all"); err == nil {
		t.Fatal("overwrote existing recording")
	}
}
func TestRecordingBackpressureIsExplicit(t *testing.T) {
	r := &recording{status: RecordStatus{State: "recording"}, frames: make(chan []byte, 1)}
	r.enqueue(make([]byte, room.FrameBytes))
	r.enqueue(make([]byte, room.FrameBytes))
	if s := r.snapshot(); s.State != "error" || s.Error == "" || !r.closed {
		t.Fatal("silent loss of recording data")
	}
}
func TestInputModesPauseEOFAndInvalidPeer(t *testing.T) {
	r, err := room.Open("127.0.0.1:0", nil, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := New(r)
	defer s.Close()
	path := filepath.Join(t.TempDir(), "wrong.wav")
	if err = s.StartRecording(path, "not-a-peer"); err == nil {
		t.Fatal("accepted unknown peer")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created file for invalid peer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); r.Run(ctx) }()
	defer func() { cancel(); <-done }()
	for _, mode := range []string{"replace", "mix"} {
		if err = s.StartInput("testdata/tone.wav", mode, false); err != nil {
			t.Fatal(err)
		}
		if err = s.StartInput("testdata/tone.wav", mode, false); err == nil {
			t.Fatal("replaced active input")
		}
		if err = s.InputAction("pause"); err != nil {
			t.Fatal(err)
		}
		pcm := bytes.Repeat([]byte{0x10, 0}, room.FrameSamples)
		s.Capture(pcm)
		if mode == "replace" && !bytes.Equal(pcm, make([]byte, room.FrameBytes)) {
			t.Fatal("paused replacement leaked microphone")
		}
		if mode == "mix" && pcm[0] != 0x10 {
			t.Fatal("paused mix suppressed microphone")
		}
		if s.Status().Input.Frames != 0 {
			t.Fatal("pause consumed file")
		}
		s.InputAction("resume")
		deadline := time.Now().Add(3 * time.Second)
		for s.Status().Input.State == "playing" && time.Now().Before(deadline) {
			s.Capture(make([]byte, room.FrameBytes))
			time.Sleep(5 * time.Millisecond)
		}
		if s.Status().Input.State != "ended" {
			t.Fatal("EOF did not restore microphone")
		}
		pcm = bytes.Repeat([]byte{0x10, 0}, room.FrameSamples)
		s.Capture(pcm)
		if pcm[0] != 0x10 {
			t.Fatal("microphone not restored")
		}
	}
}

func TestMixSaturatesAndReplaceRemovesMicrophone(t *testing.T) {
	r, err := room.Open("127.0.0.1:0", nil, bytes.Repeat([]byte{2}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s := New(r)
	for _, tc := range []struct {
		mode string
		want int16
	}{{"mix", 32767}, {"replace", 20000}} {
		pcm := make([]byte, room.FrameBytes)
		file := make([]byte, room.FrameBytes)
		for i := 0; i < len(pcm); i += 2 {
			binary.LittleEndian.PutUint16(pcm[i:], 20000)
			binary.LittleEndian.PutUint16(file[i:], 20000)
		}
		s.player = &player{status: InputStatus{State: "playing", Mode: tc.mode}, frames: make(chan fileFrame, 1)}
		s.player.frames <- fileFrame{pcm: file}
		s.Capture(pcm)
		for i := 0; i < len(pcm); i += 2 {
			if int16(binary.LittleEndian.Uint16(pcm[i:])) != tc.want {
				t.Fatalf("%s mix corrupted", tc.mode)
			}
		}
	}
}

func TestMalformedInputAndInvalidModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.wav")
	os.WriteFile(path, []byte("not a WAV"), 0600)
	if p, err := openPlayer(path, "replace", false); err == nil {
		p.stop()
		t.Fatal("accepted malformed WAV")
	}
	if p, err := openPlayer("testdata/tone.wav", "unknown", false); err == nil {
		p.stop()
		t.Fatal("accepted invalid mode")
	}
}
