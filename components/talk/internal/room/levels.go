package room

import (
	"encoding/binary"
	"math"
	"time"
)

// Level describes authenticated audio before mixing or local speaker muting.
type Level struct {
	RMS     float64 `json:"rms"`
	Peak    float64 `json:"peak"`
	Clipped bool    `json:"clipped"`
}
type levelBucket struct {
	at    int64
	level Level
}
type levelWindow struct{ buckets [4]levelBucket }

// Callers hold Room.mu. Short peaks remain visible across 100 ms CLI polls.
func (w *levelWindow) add(pcm []byte, now time.Time) {
	var energy, peak float64
	for i := 0; i+1 < len(pcm); i += 2 {
		v := math.Abs(float64(int16(binary.LittleEndian.Uint16(pcm[i:])))) / 32768
		energy += v * v
		peak = max(peak, v)
	}
	if len(pcm) < 2 {
		return
	}
	at := now.UnixMilli() / 100
	b := &w.buckets[at%4]
	if b.at != at {
		*b = levelBucket{at: at}
	}
	b.level.RMS = max(b.level.RMS, math.Sqrt(energy/float64(len(pcm)/2)))
	b.level.Peak = max(b.level.Peak, peak)
	b.level.Clipped = b.level.Clipped || peak >= 32767.0/32768
}
func (w *levelWindow) snapshot(now time.Time) Level {
	at := now.UnixMilli() / 100
	var l Level
	for _, b := range w.buckets {
		if b.at <= at && b.at > at-4 {
			l.RMS = max(l.RMS, b.level.RMS)
			l.Peak = max(l.Peak, b.level.Peak)
			l.Clipped = l.Clipped || b.level.Clipped
		}
	}
	return l
}
