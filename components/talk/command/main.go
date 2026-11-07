package command

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/cli"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/audio"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/groups"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/handset"
)

var version = "dev"

func run(ctx context.Context, args []string) error {
	dir, err := config.DefaultDir()
	if err != nil {
		return err
	}
	// --state-dir is accepted before or after the operation.
	for i := 0; i < len(args); i++ {
		if args[i] == "--state-dir" {
			if i+1 >= len(args) {
				return errors.New("--state-dir requires a path")
			}
			dir, err = filepath.Abs(args[i+1])
			if err != nil {
				return err
			}
			args = append(args[:i], args[i+2:]...)
			i--
		}
	}
	cmd := func(summary string, f func(context.Context, []string) error) cli.Command {
		return cli.Command{Summary: summary, Run: f}
	}
	app := cli.App{Name: "wirectl talk", Description: "direct encrypted microphone and speaker conversations", Commands: map[string]cli.Command{
		"call":       cmd("Dial, answer, hang up or inspect a direct connection", func(c context.Context, a []string) error { return callCommand(c, dir, a) }),
		"group":      cmd("Manage rooms, members, speaking target and listening", func(c context.Context, a []string) error { return groupCommand(c, dir, a) }),
		"record":     cmd("Record all received audio or a selected peer", func(c context.Context, a []string) error { return mediaCommand(c, dir, "record", a) }),
		"input":      cmd("Send an audio file instead of/alongside microphone", func(c context.Context, a []string) error { return mediaCommand(c, dir, "input", a) }),
		"test":       cmd("Test local input/output with live audio meters", func(c context.Context, a []string) error { return testAudioCommand(c, dir, a) }),
		"volume":     cmd("Adjust local output or member playback gain", func(c context.Context, a []string) error { return volumeCommand(c, dir, a) }),
		"levels":     cmd("Watch live input and output audio levels", func(c context.Context, a []string) error { return levelsCommand(c, dir, a) }),
		"completion": cmd("Print bash or zsh completion script", func(_ context.Context, a []string) error { return completionCommand(a) }),
		"version": cmd("Print version", func(_ context.Context, a []string) error {
			if err := noArguments("version", a); err != nil {
				return err
			}
			fmt.Println(version)
			return nil
		}),
		"init":    cmd("Create room config (never overwrites)", func(_ context.Context, a []string) error { return initConfig(dir, a) }),
		"invite":  cmd("Generate a one-use pairing code (5 minutes)", func(c context.Context, a []string) error { return inviteCommand(c, dir, a) }),
		"pair":    cmd("Save a room using HOST:PORT --code CODE, without starting audio", func(c context.Context, a []string) error { return pairCommand(c, dir, a) }),
		"devices": cmd("List audio devices in a table; --json for scripts", func(_ context.Context, a []string) error { return devicesCommand(a) }),
		"join": cmd("Join HOST:PORT --code CODE, or connect the saved room", func(c context.Context, a []string) error {
			if len(a) > 0 {
				if err := pairCommand(c, dir, a); err != nil {
					return err
				}
			}
			if os.Getenv("WIRECTL_API_CLIENT") == "1" {
				_, err := control.Request(c, dir, "POST", "/session")
				if err == nil {
					fmt.Println("Audio session connected. Use wirectl talk leave to disconnect.")
				}
				return err
			}
			return join(c, dir)
		}),
		"leave": cmd("Leave the local audio session", func(c context.Context, a []string) error {
			if err := noArguments("leave", a); err != nil {
				return err
			}
			return stopSession(c, dir)
		}),
		"status": cmd("Show readable session status; --json for scripts", func(c context.Context, a []string) error { return status(c, dir, false, a) }),
		"watch":  cmd("Watch readable status; --json for scripts; Ctrl+C only exits watch", func(c context.Context, a []string) error { return status(c, dir, true, a) }),
		"mute":   cmd("Mute local input or output", func(c context.Context, a []string) error { return muteCommand(c, dir, true, a) }),
		"unmute": cmd("Unmute local input or output", func(c context.Context, a []string) error { return muteCommand(c, dir, false, a) }),
	}}
	return app.Run(ctx, args)
}
func initConfig(dir string, args []string) error {
	c, err := config.New()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.StringVar(&c.Mode, "mode", "", "connection mode: room (default) or direct (dial before talking)")
	fs.StringVar(&c.Listen, "listen", c.Listen, "UDP listen address")
	fs.StringVar(&c.Input, "input", "", "input device ID, or none for file-only input (default device when empty)")
	fs.StringVar(&c.Output, "output", "", "output device ID (default device when empty)")
	fs.BoolVar(&c.Headphones, "headphones", false, "legacy compatibility option; audio is always full duplex")
	keyFile := fs.String("key-file", "", "read another member's room key from a private file")
	peers := fs.String("peers", "", "comma-separated reachable UDP host:port addresses")
	if err = fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected init arguments")
	}
	if *keyFile != "" {
		b, e := os.ReadFile(*keyFile)
		if e != nil {
			return e
		}
		c.Key = strings.TrimSpace(string(b))
	}
	if *peers != "" {
		for _, s := range strings.Split(*peers, ",") {
			c.Peers = append(c.Peers, strings.TrimSpace(s))
		}
	}
	if err = config.Save(dir, c); err != nil {
		return err
	}
	fmt.Println("Created", filepath.Join(dir, "config.json"))
	fmt.Println("Invite a member: wirectl talk invite. Start audio: wirectl talk join")
	return nil
}

// Run is the built-in CLI renderer invoked by the unified command bridge.
func Run(ctx context.Context, args []string, releaseVersion string) error {
	version = releaseVersion
	return run(ctx, args)
}

// Join runs the audio and room lifecycle inside the unified runtime.
func Join(ctx context.Context, dir, releaseVersion string) error {
	return joinVersion(ctx, dir, releaseVersion)
}

func join(ctx context.Context, dir string) error { return joinVersion(ctx, dir, version) }

func joinVersion(ctx context.Context, dir, version string) (result error) {
	unlock, err := control.Lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	c, err := config.Load(dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m, err := groups.Open(ctx, dir, c)
	if err != nil {
		return err
	}
	defer m.Close()
	a, err := audio.Open(c.Input, c.Output, c.Headphones, m.Capture, m.Playback)
	if err != nil {
		return err
	}
	defer func() {
		if err := closeAudioWithin(a.Close, 3*time.Second); err != nil {
			result = err
		}
	}()
	m.SetMicrophoneState(func() string {
		input, _ := a.Devices()
		switch {
		case input.Disabled:
			return "disabled"
		case !input.Online:
			return "offline"
		case a.Muted.Load():
			return "muted"
		default:
			return "ready"
		}
	})
	started := time.Now()
	keys := handset.Start(ctx, func() bool {
		for _, g := range m.Snapshot().Groups {
			if g.Current {
				return g.Mode == "direct"
			}
		}
		return false
	}, func(action string) {
		if err := m.Handset(action); err != nil {
			fmt.Fprintln(os.Stderr, "Handset:", err)
		}
	})
	defer keys.Close()
	api, err := control.Start(dir, func() any {
		input, output := a.Devices()
		snapshot := m.Snapshot()
		current := snapshot.Groups[0]
		for _, g := range snapshot.Groups {
			if g.Current {
				current = g
				break
			}
		}
		return sessionStatus{Handset: keys.Status(), OutputGainDB: snapshot.OutputGainDB, Status: current.Status, GroupID: current.RoomID, GroupName: current.Name, Groups: snapshot.Groups, Input: input.Name, Output: output.Name, InputDevice: &input, OutputDevice: &output, Muted: a.Muted.Load(), OutputMuted: a.OutputMuted.Load(), Started: started, Version: version, Media: current.Media}
	}, cancel, func(m bool) { a.Muted.Store(m) }, func(m bool) { a.OutputMuted.Store(m) }, m, m, m)
	if err != nil {
		return err
	}
	defer api.Close()
	fmt.Printf("Online in %d room(s) · audio devices reconnect automatically\n", len(m.Snapshot().Groups))
	<-ctx.Done()
	return nil
}
func stopSession(ctx context.Context, dir string) error {
	path, method := "/stop", "POST"
	if os.Getenv("WIRECTL_API_CLIENT") == "1" {
		path, method = "/session", "DELETE"
	}
	_, err := control.Request(ctx, dir, method, path)
	if err == nil {
		fmt.Println("Audio session disconnected")
	}
	return err
}
