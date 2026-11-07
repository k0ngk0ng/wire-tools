package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/audio"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
)

// levels watches the running session, so it never opens an audio device or
// interrupts a conversation. The driver reports raw microphone and played PCM.
func levelsCommand(ctx context.Context, dir string, args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println("Usage: wirectl talk levels [--state-dir DIR]")
		fmt.Println("Watch live microphone and speaker levels in the current talk session. Ctrl+C exits.")
		return nil
	}
	if len(args) != 0 {
		return errors.New("usage: levels [--state-dir DIR]")
	}
	info, _ := os.Stdout.Stat()
	interactive := info != nil && info.Mode()&os.ModeCharDevice != 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	fmt.Println("Live device levels (dBFS). Ctrl+C stops watching; talk stays online.")
	lastNames := ""
	drew := false
	defer func() {
		if drew && interactive {
			fmt.Println()
		}
	}()
	for {
		b, err := control.Request(ctx, dir, "GET", "/status")
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return unavailableStatus(dir, err)
		}
		var s sessionStatus
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("invalid session status: %w", err)
		}
		names := s.Input + "\x00" + s.Output + "\x00" + s.GroupID
		if names != lastNames {
			if drew && interactive {
				fmt.Println()
			}
			fmt.Printf("Input: %s · Output: %s\n", cleanText(s.Input), cleanText(s.Output))
			if s.GroupID != "" {
				fmt.Printf("Speaking room: %s (%s)\n", cleanText(s.GroupName), s.GroupID)
			}
			lastNames = names
		}
		line := "Input " + formatDeviceLevel(s.InputDevice, s.Muted) + " | Output " + formatDeviceLevel(s.OutputDevice, s.OutputMuted)
		if interactive {
			fmt.Print("\r\x1b[K", line)
		} else {
			fmt.Println(line)
		}
		drew = true
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func formatDeviceLevel(device *audio.DeviceState, muted bool) string {
	const width = 12
	level := audio.Level{}
	state := ""
	if device == nil || !device.Online {
		state = " offline"
		if device != nil && device.Disabled {
			state = " disabled"
		}
	} else {
		level = device.Level
		if muted {
			state = " muted"
		}
	}
	db := "-inf dBFS"
	bar := 0
	if level.RMS > 0 {
		value := 20 * math.Log10(level.RMS)
		db = fmt.Sprintf("%5.1f dBFS", value)
		bar = max(0, min(width, int(math.Round((value+60)/60*width))))
	}
	clip := ""
	if level.Clipped {
		clip = " CLIP"
	}
	return fmt.Sprintf("[%s%s] %s peak %4.1f%%%s%s", strings.Repeat("#", bar), strings.Repeat("-", width-bar), db, level.Peak*100, state, clip)
}
