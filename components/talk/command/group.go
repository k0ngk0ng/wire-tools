package command

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/groups"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/pairing"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/room"
)

func groupHelp() {
	fmt.Println(`Usage: wirectl talk group COMMAND
  list [--json]                     List saved rooms and online counts
  status [ROOM_ID|NAME] [--json]     Show a room and its online members
  levels [ROOM_ID|NAME] [--json]     Live audio meter for every room member
  watch [ROOM_ID|NAME] [--json]      Watch joins, departures and room state
  create NAME [--listen IP:PORT]    Create another room (does not switch microphone)
  join HOST:PORT --code CODE [--name NAME] [--listen IP:PORT]
                                   Join another room (does not switch microphone)
  invite [ROOM_ID|NAME]             Invite a member into a saved room
  use ROOM_ID|NAME                  Select the only room that receives your microphone
  mute [ROOM_ID|NAME]               Stop listening to a room
  unmute [ROOM_ID|NAME]             Listen to a room again
  rename ROOM_ID|NAME NEW_NAME      Change a local room label
  mode ROOM_ID|NAME room|direct     Set connection mode while talk is stopped
  leave ROOM_ID|NAME                Remove a non-current room
Omitted room means the current speaking room. Ctrl+C exits watch only.`)
}
func positionalLast(args []string) []string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return append(append([]string{}, args[1:]...), args[0])
	}
	return args
}
func groupCommand(ctx context.Context, dir string, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		groupHelp()
		return nil
	}
	action, args := args[0], args[1:]
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		groupHelp()
		return nil
	}
	switch action {
	case "levels":
		return groupLevels(ctx, dir, args)
	case "list", "status", "watch":
		return groupRead(ctx, dir, action, args)
	case "create", "join":
		return groupAdd(ctx, dir, action, args)
	case "invite":
		if len(args) > 1 {
			return fmt.Errorf("usage: group invite [ROOM_ID|NAME]")
		}
		c, err := config.Load(dir)
		if err != nil {
			return err
		}
		ref := ""
		if len(args) == 1 {
			ref = args[0]
		}
		g, err := c.FindGroup(ref)
		if err != nil {
			return err
		}
		if s, err := readGroups(ctx, dir); err == nil {
			for _, v := range s.Groups {
				if v.RoomID == g.ID() && v.Online {
					g.Listen = v.Listen
				}
			}
		}
		fmt.Printf("Room: %s (%s)\n", cleanText(g.Name), g.ID())
		return serveInvite(ctx, config.Config{Key: g.Key, Listen: g.Listen, Mode: g.Mode})
	case "mode":
		if len(args) != 2 {
			return fmt.Errorf("usage: group mode ROOM_ID|NAME room|direct (stop talk first)")
		}
		if err := changeGroup(ctx, dir, config.GroupChange{Action: "mode", Room: args[0], Mode: args[1]}); err != nil {
			return err
		}
		fmt.Println("Connection mode saved.")
		return nil
	case "use", "mute", "unmute", "rename", "leave":
		required := 1
		if action == "mute" || action == "unmute" {
			required = 0
		}
		if len(args) < required || len(args) > 1 && !(action == "rename" && len(args) == 2) || action == "rename" && len(args) != 2 {
			return fmt.Errorf("invalid group %s arguments; use group --help", action)
		}
		cmd := config.GroupChange{Action: action}
		if len(args) > 0 {
			cmd.Room = args[0]
		}
		if action == "rename" {
			cmd.Name = args[1]
		}
		if err := changeGroup(ctx, dir, cmd); err != nil {
			return err
		}
		fmt.Printf("Room %s saved.\n", action)
		s, err := readGroups(ctx, dir)
		if err != nil {
			return err
		}
		printGroupList(s)
		return nil
	default:
		return fmt.Errorf("unknown group command %q; use group --help", action)
	}
}
func readGroups(ctx context.Context, dir string) (groups.Snapshot, error) {
	b, apiErr := control.Request(ctx, dir, "GET", "/groups")
	if apiErr == nil {
		var s groups.Snapshot
		err := json.Unmarshal(b, &s)
		return s, err
	}
	unlock, err := control.Lock(dir)
	if err != nil {
		return groups.Snapshot{}, fmt.Errorf("group API unavailable; inspect wirectl api status: %w", apiErr)
	}
	defer unlock()
	c, err := config.Load(dir)
	if err != nil {
		return groups.Snapshot{}, err
	}
	s := groups.Snapshot{OutputGainDB: c.OutputGainDB, Current: c.CurrentID(), Groups: []groups.Status{}}
	for _, g := range c.RoomConfigs() {
		mode := g.Mode
		if mode == "" {
			mode = "room"
		}
		state := room.Status{Mode: mode, Listen: g.Listen, Peers: []room.Peer{}}
		if mode == "direct" {
			state.Call = &room.CallStatus{State: "offline"}
		}
		s.Groups = append(s.Groups, groups.Status{PeerGains: g.PeerGains, RoomID: g.ID(), Name: g.Name, Current: g.ID() == s.Current, Listening: !g.Muted, Status: state})
	}
	return s, nil
}
func changeGroup(ctx context.Context, dir string, cmd config.GroupChange) error {
	data, _ := json.Marshal(cmd)
	if _, err := control.RequestBody(ctx, dir, "POST", "/groups", data); err == nil {
		return nil
	} else {
		unlock, lockErr := control.Lock(dir)
		if lockErr != nil {
			return err
		}
		defer unlock()
		c, err := config.Load(dir)
		if err != nil {
			return err
		}
		next, err := c.Changed(cmd)
		if err != nil {
			return err
		}
		return config.Rewrite(dir, next)
	}
}
func chooseGroup(s groups.Snapshot, ref string) (groups.Status, error) {
	if ref == "" {
		ref = s.Current
	}
	for _, g := range s.Groups {
		if g.RoomID == ref {
			return g, nil
		}
	}
	for _, g := range s.Groups {
		if g.Name == ref {
			return g, nil
		}
	}
	return groups.Status{}, fmt.Errorf("room %q not found; use group list", ref)
}
func groupRead(ctx context.Context, dir, action string, args []string) error {
	fs := flag.NewFlagSet("group "+action, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(positionalLast(args)); err != nil {
		return err
	}
	if fs.NArg() > 1 || action == "list" && fs.NArg() != 0 {
		return fmt.Errorf("unexpected group %s arguments", action)
	}
	ref := ""
	if fs.NArg() == 1 {
		ref = fs.Arg(0)
	}
	if action == "watch" && !*asJSON {
		fmt.Println("Watching room members. Ctrl+C exits; talk stays online.")
	}
	var previous *groups.Status
	for {
		s, err := readGroups(ctx, dir)
		if err != nil {
			return err
		}
		if action == "list" {
			if *asJSON {
				b, _ := json.Marshal(s)
				fmt.Println(string(b))
			} else {
				printGroupList(s)
			}
			return nil
		}
		g, err := chooseGroup(s, ref)
		if err != nil {
			return err
		}
		if *asJSON {
			b, _ := json.Marshal(g)
			fmt.Println(string(b))
		} else if action != "watch" || previous == nil || membershipChanged(*previous, g) {
			if previous != nil && previous.RoomID == g.RoomID {
				printMemberChanges(previous.Peers, g.Peers)
			}
			printGroupStatus(g)
		}
		previous = &g
		if action != "watch" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
func membershipChanged(a, b groups.Status) bool {
	if a.RoomID != b.RoomID || a.Name != b.Name || a.Current != b.Current || a.Listening != b.Listening || a.Online != b.Online || a.Error != b.Error || a.ID != b.ID || len(a.Peers) != len(b.Peers) {
		return true
	}
	for i := range a.Peers {
		if a.Peers[i].ID != b.Peers[i].ID || a.Peers[i].Address != b.Peers[i].Address {
			return true
		}
	}
	return false
}
func printMemberChanges(old, new []room.Peer) {
	a, b := map[string]room.Peer{}, map[string]room.Peer{}
	for _, p := range old {
		a[p.ID] = p
	}
	for _, p := range new {
		b[p.ID] = p
	}
	for _, p := range new {
		if _, ok := a[p.ID]; !ok {
			fmt.Printf("+ Joined: %s (%s)\n", cleanText(p.Address), p.ID)
		}
	}
	for _, p := range old {
		if _, ok := b[p.ID]; !ok {
			fmt.Printf("- Left: %s (%s)\n", cleanText(p.Address), p.ID)
		}
	}
}
func printGroupList(s groups.Snapshot) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ROOM ID\tNAME\tSPEAK\tLISTEN\tSTATE\tOTHERS")
	for _, g := range s.Groups {
		speaking := "-"
		if g.Current {
			speaking = "current"
		}
		listening := "muted"
		if g.Listening {
			listening = "on"
		}
		state := "offline"
		if g.Online {
			state = "online"
		}
		if g.Call != nil && g.Online {
			state = g.Call.State
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n", g.RoomID, cleanText(g.Name), speaking, listening, state, len(g.Peers))
	}
	w.Flush()
}
func printGroupStatus(g groups.Status) {
	state := "Offline"
	if g.Online {
		state = "Online"
	}
	speaking := "no"
	if g.Current {
		speaking = "current"
	}
	listening := "muted"
	if g.Listening {
		listening = "on"
	}
	fmt.Printf("Room:       %s\nRoom ID:    %s\nState:      %s\nSpeaking:   %s\nListening:  %s\nListen:     %s\n", cleanText(g.Name), g.RoomID, state, speaking, listening, cleanText(g.Listen))
	if g.Call != nil {
		fmt.Printf("Connection: direct · %s\n", g.Call.State)
	}
	if g.Error != "" {
		fmt.Println("Error:     ", cleanText(g.Error))
	}
	if g.Online {
		fmt.Printf("Self:       %s\n", g.ID)
	}
	fmt.Printf("Others:     %d online\n", len(g.Peers))
	for _, p := range g.Peers {
		fmt.Printf("  %s  ID: %s\n", cleanText(p.Address), p.ID)
	}
}

// Reserve a currently available TCP/UDP port for another locally hosted room.
func groupListen() (string, error) {
	for i := 0; i < 10; i++ {
		udp, err := net.ListenPacket("udp", "0.0.0.0:0")
		if err != nil {
			return "", err
		}
		addr := udp.LocalAddr().String()
		tcp, err := net.Listen("tcp", addr)
		udp.Close()
		if err == nil {
			tcp.Close()
			return addr, nil
		}
	}
	return "", fmt.Errorf("could not find an available room port; specify --listen")
}
func groupAdd(ctx context.Context, dir, action string, args []string) error {
	fs := flag.NewFlagSet("group "+action, flag.ContinueOnError)
	name := fs.String("name", "", "local room name")
	mode := fs.String("mode", "", "connection mode for create: room or direct; join inherits invitation mode")
	listen := fs.String("listen", "", "local TCP/UDP listen address")
	code := fs.String("code", "", "one-use pairing code")
	if err := fs.Parse(positionalLast(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 || action == "join" && *code == "" {
		return fmt.Errorf("use group create NAME or group join HOST:PORT --code CODE")
	}
	if action == "join" && *mode != "" {
		return fmt.Errorf("group join inherits mode from the invitation; omit --mode")
	}
	fresh, err := config.New()
	if err != nil {
		return err
	}
	if *listen == "" {
		*listen, err = groupListen()
		if err != nil {
			return err
		}
	}
	if action == "create" {
		*name = fs.Arg(0)
	} else if *name == "" {
		*name = fs.Arg(0)
	}
	g := config.Group{Mode: *mode, Name: *name, Key: fresh.Key, Listen: *listen, Peers: []string{}}
	if err := (config.Config{Mode: g.Mode, Key: g.Key, Name: g.Name, Listen: g.Listen}).ValidateGroups(); err != nil {
		return err
	}
	old, loadErr := config.Load(dir)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	if loadErr == nil {
		if _, err := old.Changed(config.GroupChange{Action: "add", Group: &g}); err != nil {
			return err
		}
	}
	if action == "join" {
		address, err := net.ResolveUDPAddr("udp", fs.Arg(0))
		if err != nil || address.Port == 0 || len(address.IP) == 0 || address.IP.IsUnspecified() || address.IP.IsMulticast() {
			return fmt.Errorf("provide the inviter's reachable IP:port")
		}
		key, pairedMode, err := pairing.ReceiveWithMode(ctx, address.String(), *code)
		if err != nil {
			return fmt.Errorf("pairing failed: %w", err)
		}
		g.Key = base64.RawURLEncoding.EncodeToString(key)
		g.Mode = pairedMode
		g.Peers = []string{fs.Arg(0)}
	}
	if loadErr != nil {
		fresh.Key = g.Key
		fresh.Mode = g.Mode
		fresh.Listen = g.Listen
		fresh.Peers = g.Peers
		fresh.Name = g.Name
		unlock, err := control.Lock(dir)
		if err != nil {
			return err
		}
		defer unlock()
		if err = fresh.ValidateGroups(); err != nil {
			return err
		}
		if err = config.Save(dir, fresh); err != nil {
			return err
		}
	} else if err = changeGroup(ctx, dir, config.GroupChange{Action: "add", Group: &g}); err != nil {
		return err
	}
	fmt.Printf("Saved room %s (%s).\n", cleanText(g.Name), g.ID())
	if loadErr != nil {
		fmt.Println("Connect audio: wirectl talk join")
	} else {
		fmt.Printf("Select it for speaking: wirectl talk group use %s\n", g.ID())
	}
	return nil
}
