package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"golang.org/x/term"
	"math"
	"os"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/groups"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

func groupLevels(ctx context.Context, dir string, args []string) error {
	fs := flag.NewFlagSet("group levels", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON snapshots")
	if err := fs.Parse(positionalLast(args)); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: group levels [ROOM_ID|NAME] [--json]")
	}
	ref := ""
	if fs.NArg() == 1 {
		ref = fs.Arg(0)
	}
	interactive := !*asJSON && term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("TERM") != "dumb"
	if interactive {
		fmt.Print("\x1b[?1049h\x1b[?25l")
		defer fmt.Print("\x1b[?25h\x1b[?1049l")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		s, err := readGroups(ctx, dir)
		if err != nil {
			return err
		}
		g, err := chooseGroup(s, ref)
		if err != nil {
			return err
		}
		if *asJSON {
			b, _ := json.Marshal(struct {
				groups.Status
				SelfState string `json:"self_state"`
			}{g, selfAudioState(g)})
			fmt.Println(string(b))
		} else {
			if interactive {
				width, height, err := term.GetSize(int(os.Stdout.Fd()))
				if err != nil {
					width, height = 80, 24
				}
				fmt.Print(levelsScreen(formatGroupLevels(g), width, height))
			} else {
				fmt.Print(formatGroupLevels(g))
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func memberLevel(l room.Level) string {
	const width = 20
	db, n := "-inf dBFS", 0
	if l.RMS > 0 {
		value := 20 * math.Log10(l.RMS)
		db = fmt.Sprintf("%5.1f dBFS", value)
		n = max(0, min(width, int(math.Round((value+60)/60*width))))
	}
	return fmt.Sprintf("[%s%s] %s", strings.Repeat("#", n), strings.Repeat("-", width-n), db)
}
func formatGroupLevels(g groups.Status) string {
	state := "online"
	if !g.Online {
		state = "offline"
	}
	text := fmt.Sprintf("Room %s (%s) · %s\nMicrophone levels · Ctrl+C exits\n\n", cleanText(g.Name), g.RoomID, state)
	self := "Self (" + selfAudioState(g) + ")"
	text += fmt.Sprintf("%-28s %s\n", self, memberLevel(g.SelfLevel))
	for _, p := range g.Peers {
		text += fmt.Sprintf("%-28s %s\n", cleanText(p.Address), memberLevel(p.Level))
	}
	if len(g.Peers) == 0 {
		text += "No other members online.\n"
	}
	return text
}

// Describe the source feeding this room, never imply successful remote playback.
func selfAudioState(g groups.Status) string {
	if !g.Online {
		return "offline"
	}
	if !g.Current {
		return "other room"
	}
	if g.Call != nil && g.Call.State != "active" {
		return "call " + g.Call.State
	}
	if g.Media.Input.State == "playing" {
		return "file input"
	}
	switch g.MicrophoneState {
	case "muted":
		return "mic muted"
	case "offline":
		return "mic offline"
	case "disabled":
		return "mic disabled"
	}
	if g.Media.Input.State == "paused" && g.Media.Input.Mode == "replace" {
		return "file paused"
	}
	if g.SelfLevel.RMS > 0 {
		return "audio"
	}
	return "silent"
}

// One bounded write per frame. Never write beyond the last column or row: a
// wrapped newline at the bottom would scroll the alternate screen into history.
func levelsScreen(text string, width, height int) string {
	width = max(2, width)
	height = max(1, height)
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
		lines[height-1] = "More members: enlarge terminal or use --json"
	}
	var b strings.Builder
	b.WriteString("\x1b[H")
	for i, line := range lines {
		if i > 0 {
			b.WriteString("\r\n")
		}
		columns := 0
		for _, r := range line {
			n := 1
			if r > 127 {
				n = 2
			} // conservative width for wide Unicode, including room names
			if columns+n > width-1 {
				break
			}
			b.WriteRune(r)
			columns += n
		}
		b.WriteString("\x1b[K")
	}
	b.WriteString("\x1b[J")
	return b.String()
}
