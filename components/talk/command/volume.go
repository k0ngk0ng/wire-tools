package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/config"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/volume"
)

func volumeHelp() {
	fmt.Println(`Usage: wirectl talk volume [--json] [--group ROOM]
       wirectl talk volume output DB
       wirectl talk volume peer IP:PORT|NODE_ID DB [--group ROOM]
Local playback gain: -60 to +24 dB. 0 restores normal; +6 is about 2x amplitude.
Changes apply immediately and persist. Output and member gains add together.
Member gain is saved by room and IP:port, surviving node restarts at the same address.
Recording and received member meters remain unchanged. A peak limiter protects playback.`)
}
func volumeCommand(ctx context.Context, dir string, args []string) error {
	ref := ""
	asJSON := false
	pos := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			volumeHelp()
			return nil
		case "--json":
			asJSON = true
		case "--group":
			i++
			if i >= len(args) {
				return fmt.Errorf("--group needs a room ID or name")
			}
			ref = args[i]
		default:
			pos = append(pos, args[i])
		}
	}
	if len(pos) > 0 {
		cmd := config.GroupChange{Room: ref}
		var value string
		switch {
		case pos[0] == "output" && len(pos) == 2:
			if ref != "" {
				return fmt.Errorf("output gain applies to all rooms; omit --group")
			}
			cmd.Action = "output-gain"
			value = pos[1]
		case pos[0] == "peer" && len(pos) == 3:
			cmd.Action = "peer-gain"
			cmd.Peer = pos[1]
			value = pos[2]
		default:
			return fmt.Errorf("use volume output DB or volume peer IP:PORT DB; see volume --help")
		}
		db, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("invalid gain %q; use a dB value such as +6", value)
		}
		if err = volume.Validate(db); err != nil {
			return err
		}
		cmd.GainDB = &db
		if err = changeGroup(ctx, dir, cmd); err != nil {
			return err
		}
	}
	s, err := readGroups(ctx, dir)
	if err != nil {
		return err
	}
	g, err := chooseGroup(s, ref)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Output float64            `json:"output_gain_db"`
			Room   string             `json:"room_id"`
			Peers  map[string]float64 `json:"peer_gains"`
		}{s.OutputGainDB, g.RoomID, g.PeerGains})
	}
	fmt.Printf("Output gain: %+.1f dB (%.2fx)\nRoom: %s (%s)\n", s.OutputGainDB, volume.Factor(s.OutputGainDB), cleanText(g.Name), g.RoomID)
	addresses := make([]string, 0, len(g.PeerGains))
	for a := range g.PeerGains {
		addresses = append(addresses, a)
	}
	sort.Strings(addresses)
	if len(addresses) == 0 {
		fmt.Println("Member gains: all 0 dB")
	}
	for _, a := range addresses {
		fmt.Printf("  %s  %+.1f dB\n", a, g.PeerGains[a])
	}
	return nil
}
