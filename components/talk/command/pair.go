package command

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/pairing"
)

func inviteCommand(ctx context.Context, dir string, args []string) error {
	if err := noArguments("invite", args); err != nil {
		return err
	}
	c, err := config.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		// A first invitation can create the room without exposing its key.
		c, err = config.New()
		if err == nil {
			err = config.Save(dir, c)
		}
	}
	if err != nil {
		return err
	}
	g, err := c.FindGroup("")
	if err != nil {
		return err
	}
	return serveInvite(ctx, config.Config{Key: g.Key, Listen: g.Listen, Mode: g.Mode})
}

func serveInvite(ctx context.Context, c config.Config) error {
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("invalid room listen address: %w", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return errors.New("inviting requires a fixed listen port (1–65535), shared by TCP pairing and UDP audio")
	}
	key, err := c.KeyBytes()
	if err != nil {
		return err
	}
	i, err := pairing.NewWithMode(c.Listen, key, c.Mode)
	if err != nil {
		return err
	}
	defer i.Close()
	fmt.Printf("Pairing code: %s\nValid for 5 minutes, one use; keep this command open. Ctrl+C cancels.\n", i.Code)
	fmt.Printf("Listening on %s (TCP). Give the member your reachable IP:port and code.\n", i.Address())
	fmt.Println("Member: wirectl talk group join HOST:PORT --code CODE")
	if err = i.Serve(ctx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("invitation expired; run invite again for a new code")
		}
		return err
	}
	fmt.Println("Invitation consumed. Start audio with wirectl talk join, or keep your existing background session running.")
	return nil
}

func pairCommand(ctx context.Context, dir string, args []string) error {
	c, err := config.New()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("pair/join", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: wirectl talk pair|join HOST:PORT --code CODE [options]\nUse pair to save without audio; join also starts foreground audio. Existing profiles are never overwritten.")
		fs.PrintDefaults()
	}
	code := fs.String("code", "", "temporary 6-digit invitation code")
	fs.StringVar(&c.Listen, "listen", c.Listen, "local UDP listen address")
	fs.StringVar(&c.Input, "input", "", "input device ID (default when empty)")
	fs.StringVar(&c.Output, "output", "", "output device ID (default when empty)")
	fs.StringVar(&c.RingOutput, "ring-output", "", "incoming ringtone output device ID (uses call output when empty)")
	fs.BoolVar(&c.Headphones, "headphones", false, "legacy compatibility option; audio is always full duplex")
	// Support both HOST:PORT --code CODE and --code CODE HOST:PORT.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(append([]string{}, args[1:]...), args[0])
	}
	if err = fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *code == "" {
		return errors.New("usage: pair|join HOST:PORT --code CODE [--input ID --output ID --ring-output ID --headphones]")
	}
	address := fs.Arg(0)
	remote, err := net.ResolveUDPAddr("udp", address)
	if err != nil || remote.Port == 0 || len(remote.IP) == 0 || remote.IP.IsUnspecified() || remote.IP.IsMulticast() || remote.IP.Equal(net.IPv4bcast) {
		return errors.New("provide the inviter's reachable IP:port or hostname:port")
	}
	if _, err := net.ResolveUDPAddr("udp", c.Listen); err != nil {
		return fmt.Errorf("invalid local listen address: %w", err)
	}
	unlock, err := control.Lock(dir)
	if err != nil {
		return err
	}
	defer unlock()
	path := filepath.Join(dir, "config.json")
	if _, err = os.Lstat(path); err == nil {
		return errors.New("this profile already has a room; run join without arguments, or use group join HOST:PORT --code CODE for another room")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	key, mode, err := pairing.ReceiveWithMode(ctx, remote.String(), *code)
	if err != nil {
		return fmt.Errorf("pairing failed (check code, invitation and TCP reachability): %w", err)
	}
	c.Key = base64.RawURLEncoding.EncodeToString(key)
	c.Mode = mode
	c.Peers = []string{address}
	if err = config.Save(dir, c); err != nil {
		return fmt.Errorf("invitation consumed but saving the room failed: %w", err)
	}
	fmt.Println("Paired; saved room and address to", path)
	fmt.Println("Next time: wirectl talk join. No code needed.")
	return nil
}
