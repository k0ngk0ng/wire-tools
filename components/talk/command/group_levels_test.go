package command

import (
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/groups"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
	"strings"
	"testing"
)

func TestGroupLevelsShowsEachMember(t *testing.T) {
	g := groups.Status{Name: "default", RoomID: "room", Current: true, Online: true, Status: room.Status{Peers: []room.Peer{{ID: "one", Address: "10.0.0.1:51830", Level: room.Level{RMS: 0.5, Peak: 0.5}}, {ID: "two", Address: "10.0.0.2:51830"}}}}
	s := formatGroupLevels(g)
	for _, want := range []string{"Self (silent)", "10.0.0.1:51830", "10.0.0.2:51830", "-6.0 dBFS", "-inf dBFS"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q: %s", want, s)
		}
	}
}

func TestSelfStateReportsMuteOfflineAndSilence(t *testing.T) {
	g := groups.Status{Online: true, Current: true, MicrophoneState: "ready"}
	if selfAudioState(g) != "silent" {
		t.Fatal("silent room claims to send")
	}
	g.SelfLevel.RMS = .1
	if selfAudioState(g) != "audio" {
		t.Fatal("missing audio state")
	}
	for _, state := range []string{"muted", "offline", "disabled"} {
		g.MicrophoneState = state
		if selfAudioState(g) != "mic "+state {
			t.Fatal("microphone state ignored")
		}
	}
	g.Current = false
	if selfAudioState(g) != "other room" {
		t.Fatal("wrong room state")
	}
	g.Online = false
	if selfAudioState(g) != "offline" {
		t.Fatal("offline state ignored")
	}
}
func TestLevelsScreenNeverScrolls(t *testing.T) {
	screen := levelsScreen(strings.Repeat("long member name and level bar\n", 40), 20, 5)
	if strings.Count(screen, "\n") != 4 || strings.HasSuffix(screen, "\n") {
		t.Fatal("screen can scroll")
	}
	for _, line := range strings.Split(screen, "\r\n") {
		line = strings.NewReplacer("\x1b[H", "", "\x1b[K", "", "\x1b[J", "").Replace(line)
		if len(line) > 19 {
			t.Fatal("line can wrap")
		}
	}
}
