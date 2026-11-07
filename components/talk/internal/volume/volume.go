// Package volume implements local playback gain and frame peak limiting.
package volume

import (
	"encoding/binary"
	"fmt"
	"math"
	"net/netip"
)

func Validate(db float64) error {
	if math.IsNaN(db) || math.IsInf(db, 0) || db < -60 || db > 24 {
		return fmt.Errorf("gain must be between -60 and +24 dB")
	}
	return nil
}
func Factor(db float64) float64 { return math.Pow(10, db/20) }

// Address identifies a member across process restarts, including IPv4-mapped IPv6.
func Address(s string) (string, error) {
	a, err := netip.ParseAddrPort(s)
	if err != nil || !a.IsValid() || a.Port() == 0 || a.Addr().IsUnspecified() || a.Addr().IsMulticast() {
		return "", fmt.Errorf("use a member IP:port from group status")
	}
	return netip.AddrPortFrom(a.Addr().Unmap(), a.Port()).String(), nil
}

// Limiter reduces an entire frame before PCM conversion, with immediate attack
// and gradual release. It never wraps or hard-clips individual boosted samples.
type Limiter struct{ reduction float64 }

func (l *Limiter) Render(out []byte, samples []float64, gain float64) {
	peak := 0.0
	for _, v := range samples {
		peak = max(peak, math.Abs(v*gain))
	}
	target := 1.0
	if peak > 32767 {
		target = 32767 / peak
	}
	if l.reduction == 0 || target < l.reduction {
		l.reduction = target
	} else {
		l.reduction = min(target, l.reduction+0.05)
	}
	for i, v := range samples {
		value := math.Round(v * gain * l.reduction)
		value = max(-32768, min(32767, value))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(value)))
	}
}
