// Package app is the thin terminal client for the local resource API.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/api"
	"github.com/k0ngk0ng/wire-tools/components/connect"
	"github.com/k0ngk0ng/wire-tools/components/download"
	downloadcmd "github.com/k0ngk0ng/wire-tools/components/download/command"
	talkcmd "github.com/k0ngk0ng/wire-tools/components/talk/command"
	"github.com/k0ngk0ng/wire-tools/internal/host"
	"github.com/k0ngk0ng/wire-tools/internal/update"
)

const help = `wirectl — encrypted connections, downloads and conversations

Usage: wirectl [--state-dir DIR] <command> [arguments]

  connect      Pair devices and manage encrypted connections
  download     Download a URL, magnet, torrent or ED2K link
  talk         Conversations, rooms and audio devices
  update       Update the whole installation to the latest release
  api          Inspect or call the local resource API
  completion   Print bash or zsh completion
  version      Print the unified version

Resources become ready automatically when used.
WIRECTL_HOME selects a private state directory for all features.
`

func Run(ctx context.Context, args []string, version string) error {
	// Hidden adapters are invoked only by this installation's runtime or an OS
	// network helper. They are built into the same versioned executable.
	if len(args) > 0 && strings.HasPrefix(args[0], "__") {
		switch args[0] {
		case "__api":
			fs := flag.NewFlagSet("local API", flag.ContinueOnError)
			home := fs.String("state-dir", "", "private state directory")
			if err := fs.Parse(args[1:]); err != nil {
				return err
			}
			if *home == "" {
				return errors.New("missing state directory")
			}
			return host.Run(ctx, *home, version)
		case "__connect":
			run := func(c context.Context) error {
				return connect.Run(c, args[1:], version, os.Stdin, os.Stdout, os.Stderr)
			}
			if handled, err := connect.RunService(run); handled {
				return err
			}
			return run(ctx)
		case "__download-engine":
			return download.RunEngine(ctx, args[1:])
		case "__download":
			return downloadcmd.Run(ctx, args[1:], version)
		case "__talk":
			return talkcmd.Run(ctx, args[1:], version)
		default:
			return errors.New("unknown private command")
		}
	}
	home, err := api.DefaultHome()
	if err != nil {
		return err
	}
	for len(args) > 0 {
		if args[0] == "--state-dir" {
			if len(args) < 2 {
				return errors.New("--state-dir requires a path")
			}
			home, err = filepath.Abs(args[1])
			args = args[2:]
		} else if strings.HasPrefix(args[0], "--state-dir=") {
			home, err = filepath.Abs(strings.TrimPrefix(args[0], "--state-dir="))
			args = args[1:]
		} else {
			break
		}
		if err != nil {
			return err
		}
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(os.Stdout, help)
		return nil
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintln(os.Stdout, version)
		return nil
	}
	c := api.New(home)
	switch args[0] {
	case "update", "更新":
		return updateCommand(ctx, c, args[1:])
	case "api":
		return apiCommand(ctx, c, args[1:])
	case "completion":
		return completion(args[1:])
	case "connect", "download", "talk":
		feature := args[0]
		tail := args[1:]
		for _, arg := range tail {
			if arg == "--data-dir" || strings.HasPrefix(arg, "--data-dir=") || arg == "--state-dir" || strings.HasPrefix(arg, "--state-dir=") {
				return errors.New("select all feature state with wirectl --state-dir DIR or WIRECTL_HOME")
			}
		}
		// Rendering and keyboard input stay attached to the caller's terminal.
		// These frontends send every data operation through the resource API.
		if terminalFrontend(feature, tail) {
			_ = os.Setenv("WIRECTL_HOME", home)
			_ = os.Setenv("WIRECTL_API_CLIENT", "1")
			_ = os.Setenv("WIRECTL_CONNECT_HOME", filepath.Join(home, "connect"))
			_ = os.Setenv("WIRE_TALK_HOME", filepath.Join(home, "talk"))
			switch feature {
			case "download":
				return downloadcmd.Run(ctx, tail, version)
			case "talk":
				return talkcmd.Run(ctx, tail, version)
			case "connect":
				return connect.Run(ctx, tail, version, os.Stdin, os.Stdout, os.Stderr)
			}
		}

		if len(tail) > 0 && tail[0] == "update" {
			return updateCommand(ctx, c, tail[1:])
		}
		// Network setup is an OS-authorized installer operation with a real TTY;
		// credentials and connection lifetimes still belong to the local API.
		if feature == "connect" && len(tail) > 0 && tail[0] == "setup" {
			return connect.Run(ctx, append(tail, "--state-dir", filepath.Join(home, "connect")), version, os.Stdin, os.Stdout, os.Stderr)
		}
		directory, err := os.Getwd()
		if err != nil {
			return err
		}
		body, _ := json.Marshal(host.CommandRequest{Args: tail, Directory: directory, Input: ""})
		resp, err := c.Open(ctx, "POST", "/v1/commands/"+feature, body)
		if err != nil {
			return err
		}
		return host.RenderCommand(resp, os.Stdout, os.Stderr)
	default:
		return fmt.Errorf("unknown command %q (use wirectl --help)", args[0])
	}
}
func apiCommand(ctx context.Context, c *api.Client, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Println("Usage: wirectl api status [--json] | endpoint | request METHOD /v1/PATH [JSON]")
		return nil
	}
	switch args[0] {
	case "status":
		if len(args) > 2 || len(args) == 2 && args[1] != "--json" {
			return errors.New("usage: wirectl api status [--json]")
		}
		var result json.RawMessage
		if err := c.Request(ctx, "GET", "/v1/status", nil, &result); err != nil {
			return err
		}
		fmt.Println(string(result))
		return nil
	case "endpoint":
		if len(args) != 1 {
			return errors.New("usage: wirectl api endpoint")
		}
		// The endpoint file contains the bearer capability; callers discover its
		// private path rather than accidentally printing the token into logs.
		if _, err := c.Ensure(ctx); err != nil {
			return err
		}
		fmt.Println(filepath.Join(c.Home, "api.json"))
		return nil
	case "request":
		if len(args) < 3 || len(args) > 4 {
			return errors.New("usage: wirectl api request METHOD /v1/PATH [JSON]")
		}
		var data []byte
		if len(args) == 4 {
			data = []byte(args[3])
			if !json.Valid(data) {
				return errors.New("invalid JSON body")
			}
		}
		b, err := c.Do(ctx, args[1], args[2], data)
		if err != nil {
			return err
		}
		if len(b) > 0 {
			fmt.Println(string(b))
		}
		return nil
	default:
		return errors.New("usage: wirectl api status | endpoint | request METHOD PATH [JSON]")
	}
}
func updateCommand(ctx context.Context, c *api.Client, args []string) error {
	fs := flag.NewFlagSet("wirectl update", flag.ContinueOnError)
	o := update.Options{}
	fs.StringVar(&o.Version, "version", "", "release version (default latest)")
	fs.StringVar(&o.Archive, "archive", "", "offline release archive")
	fs.StringVar(&o.Checksums, "checksums", "", "trusted SHA256SUMS for offline update")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected update arguments")
	}
	// Updating is an installation transaction. Stop the old runtime after the
	// verified replacement, so subsequent calls launch the new binary.
	for _, path := range []*string{&o.Archive, &o.Checksums} {
		if *path != "" {
			absolute, err := filepath.Abs(*path)
			if err != nil {
				return err
			}
			*path = absolute
		}
	}
	old, err := c.Ensure(ctx)
	if err != nil {
		return err
	}
	var result struct {
		Version string `json:"version"`
	}
	err = c.Request(ctx, "POST", "/v1/update", map[string]string{"version": o.Version, "archive": o.Archive, "checksums": o.Checksums}, &result)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		current, err := c.Endpoint()
		if err != nil || current.Token != old.Token {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("update installed, but the previous runtime did not finish shutting down")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	fmt.Println("Installed", result.Version, "· all features use the same release")
	return nil
}
func completion(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: wirectl completion bash|zsh")
	}
	switch args[0] {
	case "bash":
		fmt.Println("complete -W 'connect download talk update api version completion' wirectl")
	case "zsh":
		fmt.Println("#compdef wirectl\n_arguments '1:command:(connect download talk update api version completion)' '*::argument:->args'")
	default:
		return errors.New("supported shells: bash, zsh")
	}
	return nil
}

func terminalFrontend(feature string, args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "help", "--help", "-h", "version", "completion":
		return true
	}
	switch feature {
	case "download":
		switch args[0] {
		case "watch", "search", "add", "list", "pause", "resume", "remove", "--detach", "login":
			return true
		}
		return strings.Contains(args[0], "://") || strings.HasPrefix(args[0], "magnet:") || strings.HasSuffix(strings.ToLower(args[0]), ".torrent")
	case "talk":
		return args[0] == "watch" || args[0] == "levels"
	case "connect":
		return args[0] != "setup" && args[0] != "serve" && args[0] != "update"
	}
	return false
}
