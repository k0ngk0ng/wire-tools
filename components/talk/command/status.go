package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/audio"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/groups"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/media"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/service"
)

type sessionStatus struct {
	Configured   *bool           `json:"configured,omitempty"`
	OutputGainDB float64         `json:"output_gain_db"`
	GroupID      string          `json:"room_id"`
	GroupName    string          `json:"room_name"`
	Groups       []groups.Status `json:"groups"`
	Media        media.Status    `json:"media"`
	room.Status
	InputDevice      *audio.DeviceState `json:"input_device,omitempty"`
	OutputDevice     *audio.DeviceState `json:"output_device,omitempty"`
	RingOutputDevice *audio.DeviceState `json:"ring_output_device,omitempty"`
	Input            string             `json:"input"`
	Output           string             `json:"output"`
	Muted            bool               `json:"muted"`
	OutputMuted      bool               `json:"output_muted"`
	Started          time.Time          `json:"started"`
	Version          string             `json:"version"`
}

func status(ctx context.Context, dir string, watch bool, args []string) error {
	name := "status"
	if watch {
		name = "watch"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON for scripts instead of readable status")
	fs.Usage = func() { fmt.Fprintf(fs.Output(), "Usage: wirectl talk %s [--json]\n", name); fs.PrintDefaults() }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: %s [--json]", name)
	}
	if watch && !*asJSON {
		fmt.Println("Watching talk. Ctrl+C stops watching; audio keeps running.")
	}
	lastError := ""
	firstSnapshot := true
	for {
		b, err := control.Request(ctx, dir, "GET", "/status")
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			firstSnapshot = true
			if !*asJSON {
				err = unavailableStatus(dir, err)
			}
			if !watch {
				return err
			}
			if *asJSON || err.Error() != lastError {
				fmt.Fprintln(os.Stderr, err)
			}
			lastError = err.Error()
		} else {
			lastError = ""
			if *asJSON {
				fmt.Print(string(b))
			} else {
				var s sessionStatus
				if err := json.Unmarshal(b, &s); err != nil {
					return fmt.Errorf("invalid session status: %w", err)
				}
				if watch && firstSnapshot {
					printStatus(s, false)
					firstSnapshot = false
				}
				printStatus(s, watch)
			}
		}
		if !watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func printStatus(s sessionStatus, watch bool) {
	if s.Configured != nil {
		if *s.Configured {
			fmt.Println("Audio session is disconnected. Run wirectl talk join to connect.")
		} else {
			fmt.Println("No room configured. Run wirectl talk init, invite, or pair HOST:PORT --code CODE.")
		}
		return
	}
	mic := "on"
	if s.Muted {
		mic = "muted"
	}
	output := "on"
	if s.OutputMuted {
		output = "muted"
	}
	if s.InputDevice != nil {
		if s.InputDevice.Disabled {
			mic = "disabled"
		} else if !s.InputDevice.Online {
			mic = "offline; reconnecting"
		}
	}
	if s.OutputDevice != nil && !s.OutputDevice.Online {
		output = "offline; reconnecting"
	}
	if watch {
		if s.Call != nil {
			fmt.Printf("Call: %s | ", s.Call.State)
		}
		fmt.Printf("%s  Online | mic: %s | output: %s | record: %s | file: %s | peers: %d | frames sent: %d received: %d dropped: %d rejected: %d\n",
			time.Now().Format("15:04:05"), mic, output, s.Media.Recording.State, s.Media.Input.State, len(s.Peers), s.Sent, s.Received, s.Dropped, s.Rejected)
		return
	}
	uptime := time.Duration(0)
	if !s.Started.IsZero() && s.Started.Before(time.Now()) {
		uptime = time.Since(s.Started).Truncate(time.Second)
	}
	fmt.Printf("State:       Online\nMicrophone:  %s (%s)\nOutput:      %s (%s)\nListen:      %s\nUptime:      %s\nVersion:     %s\n",
		cleanText(s.Input), mic, cleanText(s.Output), output, cleanText(s.Listen), uptime, cleanText(s.Version))
	fmt.Printf("Output gain: %+.1f dB\n", s.OutputGainDB)
	if s.RingOutputDevice != nil {
		state := "online"
		if !s.RingOutputDevice.Online {
			state = "offline; reconnecting"
		}
		fmt.Printf("Ringtone:    %s (%s)\n", cleanText(s.RingOutputDevice.Name), state)
	}
	if s.Call != nil {
		fmt.Printf("Connection:  direct · %s\n", s.Call.State)
	}
	if s.GroupID != "" {
		fmt.Printf("Room:        %s (%s) · current speaking room\n", cleanText(s.GroupName), s.GroupID)
	}
	fmt.Printf("Frames:      %d sent / %d received / %d dropped / %d rejected\n", s.Sent, s.Received, s.Dropped, s.Rejected)
	printMedia(s.Media)
	fmt.Printf("Node ID:     %s (current session)\n", s.ID)
	if len(s.Peers) == 0 {
		fmt.Println("Peers:       None connected yet. Invite a member with wirectl talk invite.")
		return
	}
	fmt.Printf("Peers:       %d connected\n", len(s.Peers))
	for _, p := range s.Peers {
		fmt.Printf("  %s  ID: %s\n", cleanText(p.Address), cleanText(p.ID))
	}
}

func unavailableStatus(dir string, cause error) error {
	if _, err := os.Stat(filepath.Join(dir, "config.json")); errors.Is(err, os.ErrNotExist) {
		return errors.New("no room configured; use wirectl talk invite to create one, or pair HOST:PORT --code CODE to join one")
	}
	state := "Audio is offline. Connect with wirectl talk join."
	if plan, err := service.Current(dir); err == nil {
		if _, err := os.Stat(plan.Path); err == nil {
			state = "Login service is registered, but audio is not online yet. Check devices with wirectl talk devices."
		}
	}
	if line := lastDaemonLog(dir); line != "" {
		return fmt.Errorf("%s\nLast log entry: %s", state, line)
	}
	if errors.Is(cause, os.ErrNotExist) {
		return errors.New(state)
	}
	return fmt.Errorf("%s\nDetails: %w", state, cause)
}

func lastDaemonLog(dir string) string {
	f, err := os.Open(filepath.Join(dir, "daemon.log"))
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	if st.Size() > 4096 {
		if _, err := f.Seek(st.Size()-4096, io.SeekStart); err != nil {
			return ""
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return cleanText(lines[len(lines)-1])
}

func noArguments(name string, args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Printf("Usage: wirectl talk %s [--state-dir DIR]\n", name)
		return flag.ErrHelp
	}
	if len(args) != 0 {
		return fmt.Errorf("usage: %s [--state-dir DIR]", name)
	}
	return nil
}

func muteCommand(ctx context.Context, dir string, muted bool, args []string) error {
	name, message := "unmute", "Microphone unmuted."
	if muted {
		name, message = "mute", "Microphone muted."
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Printf("Usage: wirectl talk %s [input|output] [--state-dir DIR]\nDefaults to input. Applies only to this running talk session.\n", name)
		return nil
	}
	target := "input"
	if len(args) == 1 {
		target = args[0]
	}
	if len(args) > 1 || (target != "input" && target != "output") {
		return fmt.Errorf("usage: %s [input|output]", name)
	}
	path := "/" + name
	if target == "output" {
		path += "/output"
		message = "Output unmuted."
		if muted {
			message = "Output muted."
		}
	}
	if _, err := control.Request(ctx, dir, "POST", path); err != nil {
		return fmt.Errorf("cannot %s %s: %w", name, target, err)
	}
	fmt.Println(message)
	return nil
}
