package room

import (
	"math"
	"testing"
)

func renderCallTone(state byte, count int, split bool) ([]float64, []float64) {
	r := &Room{direct: &directState{state: state}}
	out, ring := make([]float64, count), make([]float64, count)
	if split {
		r.CallToneRouted(out, ring)
	} else {
		r.CallTone(out)
	}
	return out, ring
}

func toneMagnitude(p []float64, frequency float64) float64 {
	var real, imag float64
	for i, v := range p {
		phase := 2 * math.Pi * frequency * float64(i) / SampleRate
		real += v * math.Cos(phase)
		imag -= v * math.Sin(phase)
	}
	return 2 * math.Hypot(real, imag) / float64(len(p))
}

func TestIncomingRingtoneHasTelephoneWarbleAndDoubleBurst(t *testing.T) {
	voice, ring := renderCallTone(2, SampleRate*4, true)
	for _, v := range voice {
		if v != 0 {
			t.Fatal("incoming ringtone entered handset output")
		}
	}
	for _, window := range []struct{ start, end, frequency float64 }{{.01, .04, 1100}, {.06, .09, 1500}} {
		p := ring[int(window.start*SampleRate):int(window.end*SampleRate)]
		if magnitude := toneMagnitude(p, window.frequency); magnitude < 1000 {
			t.Fatalf("incoming ringtone missing clear %.0f Hz component: %.1f", window.frequency, magnitude)
		}
	}
	for _, window := range []struct {
		start, end float64
		audible    bool
	}{{.05, .25, true}, {.43, .55, false}, {.65, .85, true}, {1.1, 2.9, false}, {3.05, 3.25, true}} {
		var power float64
		for _, v := range ring[int(window.start*SampleRate):int(window.end*SampleRate)] {
			power += v * v
		}
		if (power > 0) != window.audible {
			t.Fatalf("wrong ring cadence at %.2f..%.2f: %v", window.start, window.end, power)
		}
	}
}

func TestRingtoneClockPersistsAcrossAudioCallbacks(t *testing.T) {
	want, _ := renderCallTone(2, SampleRate*3, false)
	r := &Room{direct: &directState{state: 2}}
	got := make([]float64, len(want))
	for at := 0; at < len(got); at += FrameSamples {
		end := min(at+FrameSamples, len(got))
		r.CallTone(got[at:end])
	}
	for i, v := range want {
		if got[i] != v {
			t.Fatalf("tone clock reset at sample %d", i)
		}
	}
}

func TestRingbackAndQuietStatesRemainUnchanged(t *testing.T) {
	out, ring := renderCallTone(1, SampleRate*3, true)
	for i, v := range out {
		want := 0.0
		if i < SampleRate {
			tm := float64(i) / SampleRate
			want = 1000 * (math.Sin(2*math.Pi*440*tm) + math.Sin(2*math.Pi*480*tm))
		}
		if v != want {
			t.Fatalf("changed outgoing ringback at %d", i)
		}
		if ring[i] != 0 {
			t.Fatal("outgoing ringback entered external ringer")
		}
	}
	for _, state := range []byte{0, 3} {
		a, b := renderCallTone(state, FrameSamples, true)
		for i := range a {
			if a[i] != 0 || b[i] != 0 {
				t.Fatalf("tone in state %d", state)
			}
		}
	}
}
