package audio

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

// PCM queues tolerate different hardware clocks without accumulating latency.
// On overrun discard oldest samples; on underrun supply silence.
type pcmQueue struct {
	mu   sync.Mutex
	data []byte
}

func (q *pcmQueue) write(p []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	const capacity = room.FrameBytes * 4
	if len(p) >= capacity {
		p = p[len(p)-capacity:]
		q.data = q.data[:0]
	}
	if extra := len(q.data) + len(p) - capacity; extra > 0 {
		q.data = q.data[extra:]
	}
	q.data = append(q.data, p...)
}
func (q *pcmQueue) read(p []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := copy(p, q.data)
	clear(p[n:])
	q.data = q.data[n:]
}
func (q *pcmQueue) clear() { q.mu.Lock(); q.data = nil; q.mu.Unlock() }

type deviceHandle interface{ Close() }
type deviceFactory func(malgo.DeviceType, string, malgo.DeviceCallbacks) (deviceHandle, string, error)

type endpoint struct {
	kind   malgo.DeviceType
	id     string
	state  atomic.Pointer[DeviceState]
	pcm    pcmQueue
	levels levelWindow
}

// Keep short peaks visible between CLI polls without retaining old audio after
// silence or a disconnect. Every bucket represents 100 ms of device callbacks.
type levelWindow struct {
	mu      sync.Mutex
	buckets [4]levelBucket
}
type levelBucket struct {
	at    int64
	level Level
}

func (w *levelWindow) add(pcm []byte) {
	level := Measure(pcm)
	at := time.Now().UnixMilli() / 100
	w.mu.Lock()
	b := &w.buckets[at%int64(len(w.buckets))]
	if b.at != at {
		*b = levelBucket{at: at}
	}
	b.level.RMS = max(b.level.RMS, level.RMS)
	b.level.Peak = max(b.level.Peak, level.Peak)
	b.level.Clipped = b.level.Clipped || level.Clipped
	w.mu.Unlock()
}

func (w *levelWindow) snapshot() Level {
	now := time.Now().UnixMilli() / 100
	w.mu.Lock()
	defer w.mu.Unlock()
	var level Level
	for _, b := range w.buckets {
		if b.at <= now && b.at > now-int64(len(w.buckets)) {
			level.RMS = max(level.RMS, b.level.RMS)
			level.Peak = max(level.Peak, b.level.Peak)
			level.Clipped = level.Clipped || b.level.Clipped
		}
	}
	return level
}

func newEndpoint(kind malgo.DeviceType, id string) *endpoint {
	e := &endpoint{kind: kind, id: id}
	name := id
	if name == "" {
		name = "system default"
	}
	state := DeviceState{Name: name, Error: "waiting for device"}
	if kind == malgo.Capture && id == "none" {
		state = DeviceState{Name: "disabled (file input only)", Disabled: true}
	}
	e.state.Store(&state)
	return e
}
func (e *endpoint) snapshot() DeviceState {
	s := *e.state.Load()
	if s.Online {
		s.Level = e.levels.snapshot()
	}
	return s
}
func (e *endpoint) offline(err error) {
	s := e.snapshot()
	s.Online = false
	s.Error = err.Error()
	s.Level = Level{}
	e.state.Store(&s)
	e.pcm.clear()
}
func (e *endpoint) run(stop <-chan struct{}, factory deviceFactory) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		stopped := make(chan struct{}, 1)
		var last atomic.Int64
		last.Store(time.Now().UnixNano())
		callbacks := malgo.DeviceCallbacks{
			Data: func(out, in []byte, _ uint32) {
				last.Store(time.Now().UnixNano())
				if e.kind == malgo.Capture {
					e.levels.add(in)
					e.pcm.write(in)
				} else {
					e.pcm.read(out)
					e.levels.add(out)
				}
			},
			Stop: func() {
				select {
				case stopped <- struct{}{}:
				default:
				}
			},
		}
		var d deviceHandle
		var name string
		err := deviceFault(e.kind)
		if err == nil {
			d, name, err = factory(e.kind, e.id, callbacks)
		}
		if err == nil {
			e.state.Store(&DeviceState{Name: name, Online: true})
			watchdog := time.NewTicker(time.Second)
			active := true
			for active {
				select {
				case <-stop:
					active = false
				case <-stopped:
					err = fmt.Errorf("device disconnected; retrying")
					active = false
				case <-watchdog.C:
					if fault := deviceFault(e.kind); fault != nil {
						err = fault
						active = false
						break
					}
					if time.Since(time.Unix(0, last.Load())) > 2*time.Second {
						err = fmt.Errorf("device stopped delivering audio; retrying")
						active = false
					}
				}
			}
			watchdog.Stop()
			if err == nil {
				err = fmt.Errorf("stopped")
			}
			e.offline(err)
			// A stuck native teardown is confined to this endpoint. Never open another
			// copy until it releases the hardware; the other direction and room live on.
			d.Close()
		}
		e.offline(err)
		select {
		case <-stop:
			return
		case <-time.After(time.Second):
		}
	}
}

type nativeDevice struct {
	context *malgo.AllocatedContext
	device  *malgo.Device
}

func (d *nativeDevice) Close() { d.device.Uninit(); d.context.Uninit(); d.context.Free() }
func nativeFactory(backends []malgo.Backend) deviceFactory {
	return func(kind malgo.DeviceType, id string, callbacks malgo.DeviceCallbacks) (deviceHandle, string, error) {
		ctx, err := malgo.InitContext(backends, malgo.ContextConfig{}, nil)
		if err != nil {
			return nil, "", err
		}
		fail := func(err error) (deviceHandle, string, error) { ctx.Uninit(); ctx.Free(); return nil, "", err }
		devices, err := ctx.Devices(kind)
		if err != nil {
			return fail(err)
		}
		cfg := malgo.DefaultDeviceConfig(kind)
		cfg.Alsa.NoMMap = 1
		cfg.SampleRate = room.SampleRate
		cfg.PeriodSizeInFrames = room.FrameSamples
		name := ""
		for _, d := range devices {
			if (id == "" && d.IsDefault != 0) || d.ID.String() == id {
				ptr := d.ID.Pointer()
				defer freeDeviceID(ptr)
				if kind == malgo.Capture {
					cfg.Capture.DeviceID = ptr
				} else {
					cfg.Playback.DeviceID = ptr
				}
				name = d.Name()
				break
			}
		}
		if name == "" {
			return fail(fmt.Errorf("configured device unavailable; reconnect it or select a device with talk devices"))
		}
		cfg.Capture.Format = malgo.FormatS16
		cfg.Capture.Channels = 1
		cfg.Playback.Format = malgo.FormatS16
		cfg.Playback.Channels = 1
		d, err := malgo.InitDevice(ctx.Context, cfg, callbacks)
		if err != nil {
			return fail(err)
		}
		if err = d.Start(); err != nil {
			d.Uninit()
			return fail(err)
		}
		return &nativeDevice{ctx, d}, name, nil
	}
}
