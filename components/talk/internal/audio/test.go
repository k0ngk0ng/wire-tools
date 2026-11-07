package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/gen2brain/malgo"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

type Level struct {
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
	Clipped bool    `json:"clipped"`
}

func Measure(pcm []byte) Level {
	var sum, peak float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i:]))) / 32768)
		sum += v * v
		if v > peak {
			peak = v
		}
	}
	rms := 0.0
	if len(pcm) >= 2 {
		rms = math.Sqrt(sum / float64(len(pcm)/2))
	}
	return Level{RMS: rms, Peak: peak, Clipped: peak >= 0.999}
}

// TestDevice opens only the requested direction. Output tests use a quiet 440Hz
// pulsed tone with fades; capture tests never play or transmit microphone audio.
func TestDevice(ctx context.Context, kind, id string, report func(string, Level)) error {
	dtype := malgo.Capture
	if kind == "output" {
		dtype = malgo.Playback
	} else if kind != "input" {
		return fmt.Errorf("test direction must be input or output")
	}
	c, err := malgo.InitContext(nativeBackends(), malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	defer c.Free()
	defer c.Uninit()
	ds, err := c.Devices(dtype)
	if err != nil {
		return err
	}
	var deviceID unsafe.Pointer
	name := ""
	for _, d := range ds {
		if (id == "" && d.IsDefault != 0) || d.ID.String() == id {
			deviceID = d.ID.Pointer()
			name = d.Name()
			break
		}
	}
	if name == "" {
		return fmt.Errorf("%s device not found; use talk devices and --device ID", kind)
	}
	// Free via the same allocator used by malgo DeviceID.Pointer.
	defer freeDeviceID(deviceID)
	cfg := malgo.DefaultDeviceConfig(dtype)
	// BCM2835 ALSA mmap playback can stall with a stationary hardware pointer.
	// Use the read/write path, as in malgo's capture/playback examples.
	cfg.Alsa.NoMMap = 1
	cfg.SampleRate = room.SampleRate
	cfg.PeriodSizeInFrames = room.FrameSamples
	if dtype == malgo.Capture {
		cfg.Capture.DeviceID = deviceID
		cfg.Capture.Format = malgo.FormatS16
		cfg.Capture.Channels = 1
	} else {
		cfg.Playback.DeviceID = deviceID
		cfg.Playback.Format = malgo.FormatS16
		cfg.Playback.Channels = 1
	}
	var rms, peak atomic.Uint64
	var clipped atomic.Bool
	var frames atomic.Uint64
	var sample uint64
	stopped := make(chan struct{}, 1)
	d, err := malgo.InitDevice(c.Context, cfg, malgo.DeviceCallbacks{Data: func(out, in []byte, _ uint32) {
		pcm := in
		if dtype == malgo.Playback {
			pcm = out
			for i := 0; i+1 < len(out); i += 2 {
				phase := float64(sample%room.SampleRate) / room.SampleRate
				envelope := 0.0
				if phase < 0.7 {
					envelope = math.Min(1, math.Min(phase/0.02, (0.7-phase)/0.02))
				}
				value := int16(0.12 * 32767 * envelope * math.Sin(2*math.Pi*440*float64(sample)/room.SampleRate))
				binary.LittleEndian.PutUint16(out[i:], uint16(value))
				sample++
			}
		}
		level := Measure(pcm)
		rms.Store(math.Float64bits(level.RMS))
		peak.Store(math.Float64bits(level.Peak))
		if level.Clipped {
			clipped.Store(true)
		}
		frames.Add(1)
	}, Stop: func() {
		select {
		case stopped <- struct{}{}:
		default:
		}
	}})
	if err != nil {
		return err
	}
	defer d.Uninit()
	if err = d.Start(); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if frames.Load() == 0 {
				return fmt.Errorf("device produced no audio callbacks")
			}
			return nil
		case <-stopped:
			return fmt.Errorf("audio device disconnected or stopped")
		case <-ticker.C:
			report(name, Level{RMS: math.Float64frombits(rms.Load()), Peak: math.Float64frombits(peak.Load()), Clipped: clipped.Swap(false)})
		}
	}
}
