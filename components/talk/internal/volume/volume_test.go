package volume

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestGainAndLimiter(t *testing.T) {
	out := make([]byte, 8)
	var l Limiter
	l.Render(out, []float64{1000, -1000, 0, 500}, Factor(6))
	if v := int16(binary.LittleEndian.Uint16(out)); v != 1995 {
		t.Fatalf("+6dB = %d", v)
	}
	l.Render(out, []float64{10000, -10000, 5000, 0}, 10)
	a := int16(binary.LittleEndian.Uint16(out))
	b := int16(binary.LittleEndian.Uint16(out[2:]))
	c := int16(binary.LittleEndian.Uint16(out[4:]))
	if a != 32767 || b != -32767 || math.Abs(float64(c)*2-float64(a)) > 1 {
		t.Fatalf("limiting must preserve frame waveform: %d %d %d", a, b, c)
	}
	l.Render(out, []float64{0, 0, 0, 0}, 10)
	for _, v := range out {
		if v != 0 {
			t.Fatal("gain introduced noise")
		}
	}
	for _, db := range []float64{math.NaN(), math.Inf(1), 25, -61} {
		if Validate(db) == nil {
			t.Fatal("accepted invalid gain")
		}
	}
	address, err := Address("[::ffff:100.113.200.13]:51830")
	if err != nil || address != "100.113.200.13:51830" {
		t.Fatal(address, err)
	}
}
