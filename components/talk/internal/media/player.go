package media

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/gopxl/beep"
	"github.com/gopxl/beep/flac"
	"github.com/gopxl/beep/mp3"
	"github.com/gopxl/beep/wav"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

type InputStatus struct {
	State     string `json:"state"`
	File      string `json:"file,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Loop      bool   `json:"loop"`
	Frames    uint64 `json:"frames"`
	Underruns uint64 `json:"underruns"`
	Error     string `json:"error,omitempty"`
}
type fileFrame struct {
	pcm []byte
	err error
	end bool
}
type player struct {
	status InputStatus
	frames chan fileFrame
	cancel context.CancelFunc
	done   chan struct{}
}

func openPlayer(path, mode string, loop bool) (*player, error) {
	if mode != "replace" && mode != "mix" {
		return nil, fmt.Errorf("mode must be replace or mix")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("input must be a regular audio file")
	}
	var decoder beep.StreamSeekCloser
	var format beep.Format
	switch strings.ToLower(filepath.Ext(path)) {
	case ".wav":
		decoder, format, err = wav.Decode(f)
	case ".mp3":
		decoder, format, err = mp3.Decode(f)
	case ".flac":
		decoder, format, err = flac.Decode(f)
	default:
		f.Close()
		return nil, fmt.Errorf("supported input formats: WAV, MP3, FLAC")
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	if format.SampleRate <= 0 || format.SampleRate > 384000 || format.NumChannels < 1 || format.NumChannels > 2 {
		decoder.Close()
		return nil, fmt.Errorf("input must be mono/stereo audio at up to 384 kHz")
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &player{status: InputStatus{State: "playing", File: path, Mode: mode, Loop: loop}, frames: make(chan fileFrame, 8), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		defer decoder.Close()
		send := func(frame fileFrame) bool {
			select {
			case p.frames <- frame:
				return true
			case <-ctx.Done():
				return false
			}
		}
		stream := beep.Resample(4, format.SampleRate, beep.SampleRate(room.SampleRate), decoder)
		samples := make([][2]float64, room.FrameSamples)
		cycleSamples := 0
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			n, _ := stream.Stream(samples)
			if n > 0 {
				cycleSamples += n
				pcm := make([]byte, room.FrameBytes)
				for i := 0; i < n; i++ {
					v := (samples[i][0] + samples[i][1]) / 2
					if math.IsNaN(v) {
						v = 0
					}
					v = math.Max(-1, math.Min(1, v))
					value := int32(math.Round(v * 32768))
					if value > 32767 {
						value = 32767
					}
					binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(value)))
				}
				if !send(fileFrame{pcm: pcm}) {
					return
				}
			}
			if n < len(samples) {
				if err := stream.Err(); err != nil {
					send(fileFrame{end: true, err: err})
					return
				}
				if !loop || cycleSamples == 0 {
					send(fileFrame{end: true})
					return
				}
				if err := decoder.Seek(0); err != nil {
					send(fileFrame{end: true, err: err})
					return
				}
				stream = beep.Resample(4, format.SampleRate, beep.SampleRate(room.SampleRate), decoder)
				cycleSamples = 0
			}
		}
	}()
	return p, nil
}
func (p *player) stop() { p.cancel(); <-p.done }
