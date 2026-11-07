package groups

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/volume"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

func await(t *testing.T, check func() bool) {
	t.Helper()
	end := time.Now().Add(4 * time.Second)
	for time.Now().Before(end) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not reached")
}
func frame(value uint16) []byte {
	p := make([]byte, room.FrameBytes)
	for i := 0; i < len(p); i += 2 {
		binary.LittleEndian.PutUint16(p[i:], value)
	}
	return p
}
func TestIndependentRoutingAndListening(t *testing.T) {
	dir := t.TempDir()
	c, _ := config.New()
	c.Listen = "127.0.0.1:0"
	c.Name = "alpha"
	other, _ := config.New()
	g := config.Group{Name: "beta", Key: other.Key, Listen: "127.0.0.1:0"}
	c.Groups = []config.Group{g}
	if err := config.Save(dir, c); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m, err := Open(ctx, dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	var peers []*room.Room
	for _, group := range c.RoomConfigs() {
		key, _ := (config.Config{Key: group.Key}).KeyBytes()
		r, err := room.Open("127.0.0.1:0", []string{m.rooms[group.ID()].r.Status().Listen}, key)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { defer close(done); r.Run(ctx) }()
		defer func() { r.Close(); <-done }()
		peers = append(peers, r)
	}
	await(t, func() bool { s := m.Snapshot(); return len(s.Groups[0].Peers) == 1 && len(s.Groups[1].Peers) == 1 })
	for i := 0; i < 5; i++ {
		m.Capture(frame(1000))
		time.Sleep(20 * time.Millisecond)
	}
	await(t, func() bool { return peers[0].Status().Received >= 3 })
	if peers[1].Status().Received != 0 {
		t.Fatal("microphone leaked into non-current room")
	}
	if err = m.Apply(config.GroupChange{Action: "use", Room: g.ID()}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	before := peers[0].Status().Received
	for i := 0; i < 5; i++ {
		m.Capture(frame(2000))
		time.Sleep(20 * time.Millisecond)
	}
	await(t, func() bool { return peers[1].Status().Received >= 3 })
	if peers[0].Status().Received != before {
		t.Fatal("microphone still sent to previous room")
	}
	out := make([]byte, room.FrameBytes)
	feed := func() {
		for i := 0; i < 3; i++ {
			peers[0].Capture(frame(1000))
			peers[1].Capture(frame(2000))
			time.Sleep(20 * time.Millisecond)
		}
	}
	feed()
	await(t, func() bool { m.Playback(out); return int16(binary.LittleEndian.Uint16(out)) == 3000 })
	if err = m.Apply(config.GroupChange{Action: "mute", Room: "alpha"}); err != nil {
		t.Fatal(err)
	}
	feed()
	await(t, func() bool { m.Playback(out); return int16(binary.LittleEndian.Uint16(out)) == 2000 })
	if err = m.Apply(config.GroupChange{Action: "unmute", Room: "alpha"}); err != nil {
		t.Fatal(err)
	}
	feed()
	await(t, func() bool { m.Playback(out); return int16(binary.LittleEndian.Uint16(out)) == 3000 })
	db := 6.0
	if err = m.Apply(config.GroupChange{Action: "peer-gain", Room: "alpha", Peer: peers[0].Status().ID, GainDB: &db}); err != nil {
		t.Fatal(err)
	}
	if err = m.Apply(config.GroupChange{Action: "output-gain", GainDB: &db}); err != nil {
		t.Fatal(err)
	}
	feed()
	want := int16(math.Round((1000*volume.Factor(6) + 2000) * volume.Factor(6)))
	await(t, func() bool { m.Playback(out); return int16(binary.LittleEndian.Uint16(out)) == want })
	gainCfg, err := config.Load(dir)
	if err != nil || gainCfg.OutputGainDB != 6 || len(gainCfg.PeerGains) != 1 {
		t.Fatal("live gains did not persist", err)
	}
	b, _ := json.Marshal(m.Snapshot())
	var public map[string]any
	if err = json.Unmarshal(b, &public); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{c.Key, other.Key} {
		if strings.Contains(string(b), secret) {
			t.Fatal("status exposed a room key")
		}
	}
	saved, err := config.Load(dir)
	if err != nil || saved.CurrentID() != g.ID() {
		t.Fatal("speaking target did not persist", err)
	}
}
