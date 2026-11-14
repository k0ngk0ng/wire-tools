// Package audio manages capture and playback independently of the room connection.
package audio

import (
	"github.com/gen2brain/malgo"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
	"sync"
	"sync/atomic"
	"time"
)

type Device struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

type DeviceState struct {
	Name     string `json:"name"`
	Online   bool   `json:"online"`
	Error    string `json:"error,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Level    Level  `json:"level"`
}

type Audio struct {
	Muted         atomic.Bool
	OutputMuted   atomic.Bool
	input, output *endpoint
	ring          *endpoint
	stop          chan struct{}
	done          chan struct{}
	workers       sync.WaitGroup
	once          sync.Once
}

func List() ([]Device, error) {
	ctx, err := malgo.InitContext(nativeBackends(), malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, err
	}
	defer ctx.Free()
	defer ctx.Uninit()
	out := []Device{}
	for _, kind := range []malgo.DeviceType{malgo.Capture, malgo.Playback} {
		ds, err := ctx.Devices(kind)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			label := "input"
			if kind == malgo.Playback {
				label = "output"
			}
			out = append(out, Device{label, publicDeviceID(d.ID.String()), d.Name(), d.IsDefault != 0})
		}
	}
	return append(out, additionalDevices()...), nil
}

// OpenWithRinger keeps the incoming alert on its own reconnecting endpoint.
// An omitted/shared ring device preserves the original single-output behavior.
func OpenWithRinger(input, output, ring string, capture func([]byte), playback func([]byte, []byte)) (*Audio, error) {
	if output == "" && ring != "" {
		// An explicit ID may refer to the system default call output. Avoid
		// opening the same exclusive native device a second time in that case.
		if devices, err := List(); err == nil {
			for _, d := range devices {
				if d.Kind == "output" && d.Default && d.ID == ring {
					ring = ""
					break
				}
			}
		}
	}
	return openRoutedWithFactory(nativeFactory(nativeBackends()), input, output, ring, capture, playback), nil
}

func Open(input, output string, headphones bool, capture func([]byte), playback func([]byte)) (*Audio, error) {
	return openWithBackends(nativeBackends(), input, output, headphones, capture, playback)
}

func openWithBackends(backends []malgo.Backend, input, output string, headphones bool, capture func([]byte), playback func([]byte)) (*Audio, error) {
	return openWithFactory(nativeFactory(backends), input, output, headphones, capture, playback), nil
}

// The legacy headphones parameter is retained for config/API compatibility.
// Playback must never gate capture: remote noise otherwise starves local speech.
func openWithFactory(factory deviceFactory, input, output string, _ bool, capture func([]byte), playback func([]byte)) *Audio {
	return openRoutedWithFactory(factory, input, output, "", capture, func(out, _ []byte) { playback(out) })
}

func openRoutedWithFactory(factory deviceFactory, input, output, ring string, capture func([]byte), playback func([]byte, []byte)) *Audio {
	a := &Audio{stop: make(chan struct{}), done: make(chan struct{})}
	a.input = newEndpoint(malgo.Capture, input)
	a.output = newEndpoint(malgo.Playback, output)
	if ring != "" && ring != output {
		a.ring = newEndpoint(malgo.Playback, ring)
		a.ring.ringer = true
	}
	for _, e := range []*endpoint{a.input, a.output, a.ring} {
		if e == nil || e.snapshot().Disabled {
			continue
		}
		a.workers.Add(1)
		go func(e *endpoint) { defer a.workers.Done(); e.run(a.stop, factory) }(e)
	}
	// A software clock keeps received recording, file input and UDP audio alive
	// even when a native device is absent. Native callbacks only move bounded PCM.
	go func() {
		defer close(a.done)
		ticker := time.NewTicker(time.Duration(room.FrameSamples) * time.Second / room.SampleRate)
		defer ticker.Stop()
		in, out := make([]byte, room.FrameBytes), make([]byte, room.FrameBytes)
		var ring []byte
		if a.ring != nil {
			ring = make([]byte, room.FrameBytes)
		}
		for {
			select {
			case <-a.stop:
				return
			case <-ticker.C:
				a.input.pcm.read(in)
				if a.Muted.Load() || !a.input.snapshot().Online {
					clear(in)
				}
				capture(in)
				playback(out, ring)
				if a.OutputMuted.Load() {
					clear(out)
					clear(ring)
				}
				if a.output.snapshot().Online {
					a.output.pcm.write(out)
				}
				if a.ring != nil && a.ring.snapshot().Online {
					a.ring.pcm.write(ring)
				}
			}
		}
	}()
	return a
}

func (a *Audio) Devices() (DeviceState, DeviceState) { return a.input.snapshot(), a.output.snapshot() }
func (a *Audio) RingDevice() *DeviceState {
	if a.ring == nil {
		return nil
	}
	s := a.ring.snapshot()
	return &s
}
func (a *Audio) Close() {
	a.once.Do(func() { close(a.stop) })
	<-a.done
	beforeDeviceClose()
	a.workers.Wait()
}

// copyPlayback consumes the same audio while muted, preventing queued speech
// from being replayed when output is enabled again.
func copyPlayback(out, frame []byte, muted bool) int {
	n := copy(out, frame)
	if muted {
		clear(out[:n])
	}
	return n
}
