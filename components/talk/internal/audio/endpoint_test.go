package audio

import (
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gen2brain/malgo"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

type fakeAudioDevice struct {
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	teardown <-chan struct{}
}

func (d *fakeAudioDevice) Close() {
	d.once.Do(func() { close(d.stop) })
	<-d.done
	if d.teardown != nil {
		<-d.teardown
	}
}
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if f() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}

func TestMicrophoneDisconnectKeepsPlaybackAndRecovers(t *testing.T) {
	var available atomic.Bool
	available.Store(true)
	var inputStarts, outputStarts, heard atomic.Int32
	var captures, plays atomic.Int32
	factory := func(kind malgo.DeviceType, _ string, cb malgo.DeviceCallbacks) (deviceHandle, string, error) {
		if kind == malgo.Capture && !available.Load() {
			return nil, "", errors.New("unplugged")
		}
		if kind == malgo.Capture {
			inputStarts.Add(1)
		} else {
			outputStarts.Add(1)
		}
		d := &fakeAudioDevice{stop: make(chan struct{}), done: make(chan struct{})}
		go func() {
			defer close(d.done)
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			p := make([]byte, room.FrameBytes)
			for {
				select {
				case <-d.stop:
					return
				case <-ticker.C:
					if kind == malgo.Capture {
						if !available.Load() {
							cb.Stop()
							return
						}
						for i := range p {
							p[i] = 1
						}
						cb.Data(nil, p, room.FrameSamples)
					} else {
						cb.Data(p, nil, room.FrameSamples)
						if Measure(p).RMS > 0 {
							heard.Add(1)
						}
					}
				}
			}
		}()
		return d, "test device", nil
	}
	a := openWithFactory(factory, "mic", "speaker", true, func(p []byte) {
		if Measure(p).RMS > 0 {
			captures.Add(1)
		}
	}, func(p []byte) {
		for i := range p {
			p[i] = 2
		}
		plays.Add(1)
	})
	defer a.Close()
	eventually(t, func() bool { return captures.Load() > 2 && heard.Load() > 2 })
	eventually(t, func() bool {
		input, output := a.Devices()
		return input.Level.RMS > 0 && output.Level.RMS > 0
	})
	available.Store(false)
	eventually(t, func() bool { i, o := a.Devices(); return !i.Online && o.Online })
	if input, _ := a.Devices(); input.Level.RMS != 0 {
		t.Fatal("disconnected microphone retained a live level")
	}
	before := heard.Load()
	oldCapture := captures.Load()
	eventually(t, func() bool { return heard.Load() > before+10 })
	if captures.Load() != oldCapture {
		t.Fatal("stale microphone audio sent while offline")
	}
	a.Muted.Store(true)
	available.Store(true)
	eventually(t, func() bool { i, _ := a.Devices(); return i.Online && inputStarts.Load() >= 2 })
	time.Sleep(100 * time.Millisecond)
	if captures.Load() != oldCapture {
		t.Fatal("reconnect cleared microphone mute")
	}
	a.Muted.Store(false)
	eventually(t, func() bool { return captures.Load() > oldCapture+2 })
	if outputStarts.Load() != 1 {
		t.Fatal("microphone reconnect reopened output")
	}
}

func TestNoDevicesStillClocksMediaAndCanStop(t *testing.T) {
	var capture, playback atomic.Int32
	a := openWithFactory(func(malgo.DeviceType, string, malgo.DeviceCallbacks) (deviceHandle, string, error) {
		return nil, "", errors.New("absent")
	}, "missing", "missing", true, func(p []byte) {
		capture.Add(1)
		if Measure(p).RMS > 0 {
			t.Error("offline capture not silent")
		}
	}, func(p []byte) { clear(p); playback.Add(1) })
	eventually(t, func() bool { return capture.Load() >= 3 && playback.Load() >= 3 })
	a.Close()
	c := capture.Load()
	time.Sleep(50 * time.Millisecond)
	if capture.Load() != c {
		t.Fatal("clock continued after shutdown")
	}
}

func TestPCMQueueDropsStaleAudio(t *testing.T) {
	var q pcmQueue
	q.write(make([]byte, room.FrameBytes*4))
	fresh := make([]byte, room.FrameBytes*4)
	for i := range fresh {
		fresh[i] = 42
	}
	q.write(fresh)
	out := make([]byte, len(fresh)+2)
	q.read(out)
	for _, b := range out[:len(fresh)] {
		if b != 42 {
			t.Fatal("stale audio kept")
		}
	}
	if out[len(fresh)] != 0 || out[len(fresh)+1] != 0 {
		t.Fatal("underrun not silent")
	}
}

func TestLevelWindowTracksRecentAudioOnly(t *testing.T) {
	var window levelWindow
	pcm := make([]byte, room.FrameBytes)
	for i := range pcm {
		pcm[i] = 40
	}
	window.add(pcm)
	if level := window.snapshot(); level.RMS == 0 || level.Peak == 0 {
		t.Fatalf("captured audio not visible: %+v", level)
	}
	time.Sleep(450 * time.Millisecond)
	if level := window.snapshot(); level.RMS != 0 || level.Peak != 0 {
		t.Fatalf("stale audio remained visible: %+v", level)
	}
}

func TestStuckCaptureTeardownDoesNotStopOutput(t *testing.T) {
	release := make(chan struct{})
	var playback atomic.Int32
	callbacks := make(chan malgo.DeviceCallbacks, 1)
	factory := func(kind malgo.DeviceType, _ string, cb malgo.DeviceCallbacks) (deviceHandle, string, error) {
		d := &fakeAudioDevice{stop: make(chan struct{}), done: make(chan struct{})}
		close(d.done)
		if kind == malgo.Capture {
			d.teardown = release
			callbacks <- cb
		}
		return d, "test device", nil
	}
	a := openWithFactory(factory, "mic", "speaker", true, func([]byte) {}, func(p []byte) { clear(p); playback.Add(1) })
	defer func() { close(release); a.Close() }()
	cb := <-callbacks
	cb.Stop()
	eventually(t, func() bool { i, _ := a.Devices(); return !i.Online })
	n := playback.Load()
	eventually(t, func() bool { return playback.Load() > n+5 })
	_, o := a.Devices()
	if !o.Online {
		t.Fatal("stuck microphone teardown took output offline")
	}
}

func TestReceiverNoiseDoesNotSuppressMicrophone(t *testing.T) {
	var received atomic.Int32
	var playbackSample atomic.Int32
	playbackSample.Store(180)
	factory := func(kind malgo.DeviceType, _ string, cb malgo.DeviceCallbacks) (deviceHandle, string, error) {
		d := &fakeAudioDevice{stop: make(chan struct{}), done: make(chan struct{})}
		go func() {
			defer close(d.done)
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			pcm := make([]byte, room.FrameBytes)
			for {
				select {
				case <-d.stop:
					return
				case <-ticker.C:
					if kind == malgo.Capture {
						for i := 0; i < len(pcm); i += 2 {
							pcm[i] = 232
							pcm[i+1] = 3
						}
						cb.Data(nil, pcm, room.FrameSamples)
					} else {
						cb.Data(pcm, nil, room.FrameSamples)
					}
				}
			}
		}()
		return d, "test device", nil
	}
	a := openWithFactory(factory, "mic", "speaker", false, func(p []byte) {
		if Measure(p).RMS > 0 {
			received.Add(1)
		}
	}, func(p []byte) {
		sample := uint16(playbackSample.Load())
		for i := 0; i < len(p); i += 2 {
			p[i] = byte(sample)
			p[i+1] = byte(sample >> 8)
		}
	})
	defer a.Close()
	eventually(t, func() bool { return received.Load() > 10 })
	playbackSample.Store(1000)
	time.Sleep(150 * time.Millisecond)
	before := received.Load()
	eventually(t, func() bool { return received.Load() > before+5 })
	playbackSample.Store(180)
	eventually(t, func() bool { return received.Load() > before+5 })
}

// Reproduces the production failure: continuous remote audio used to renew the
// guard forever, silencing a working microphone in the default speaker mode.
func TestContinuousPlaybackNeverSuppressesMicrophone(t *testing.T) {
	var captured, heard atomic.Int32
	factory := func(kind malgo.DeviceType, _ string, cb malgo.DeviceCallbacks) (deviceHandle, string, error) {
		d := &fakeAudioDevice{stop: make(chan struct{}), done: make(chan struct{})}
		go func() {
			defer close(d.done)
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			p := make([]byte, room.FrameBytes)
			for {
				select {
				case <-d.stop:
					return
				case <-tick.C:
					if kind == malgo.Capture {
						for i := 0; i < len(p); i += 2 {
							binary.LittleEndian.PutUint16(p[i:], 1000)
						}
						cb.Data(nil, p, room.FrameSamples)
					} else {
						cb.Data(p, nil, room.FrameSamples)
						if Measure(p).RMS > 0 {
							heard.Add(1)
						}
					}
				}
			}
		}()
		return d, "test", nil
	}
	a := openWithFactory(factory, "mic", "speaker", false, func(p []byte) {
		if Measure(p).RMS > 0 {
			captured.Add(1)
		}
	}, func(p []byte) {
		for i := 0; i < len(p); i += 2 {
			binary.LittleEndian.PutUint16(p[i:], 20000)
		}
	})
	defer a.Close()
	eventually(t, func() bool { return captured.Load() > 15 && heard.Load() > 15 })
	before := captured.Load()
	eventually(t, func() bool { return captured.Load() > before+15 })
	a.Muted.Store(true)
	time.Sleep(80 * time.Millisecond)
	before = captured.Load()
	time.Sleep(120 * time.Millisecond)
	if captured.Load() != before {
		t.Fatal("explicit microphone mute ignored")
	}
	a.Muted.Store(false)
	eventually(t, func() bool { return captured.Load() > before+5 })
	a.OutputMuted.Store(true)
	time.Sleep(100 * time.Millisecond)
	plays := heard.Load()
	before = captured.Load()
	time.Sleep(150 * time.Millisecond)
	if heard.Load() != plays || captured.Load() <= before {
		t.Fatal("output mute affected microphone or failed to mute playback")
	}
	a.OutputMuted.Store(false)
	eventually(t, func() bool { return heard.Load() > plays+5 && captured.Load() > before+5 })
}
