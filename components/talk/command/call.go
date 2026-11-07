package command

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
)

func callCommand(ctx context.Context, dir string, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Println("Usage: wirectl talk call dial|answer|hangup|button|status [--group ROOM] [--json]")
		return nil
	}
	fs := flag.NewFlagSet("call", flag.ContinueOnError)
	ref := fs.String("group", "", "connection ID or name (default current)")
	asJSON := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected call arguments")
	}
	switch args[0] {
	case "status":
	case "dial", "answer", "hangup", "button":
		body, _ := json.Marshal(map[string]string{"action": args[0], "group": *ref})
		if _, err := control.RequestBody(ctx, dir, "POST", "/call", body); err != nil {
			return err
		}
	default:
		return fmt.Errorf("use call dial, answer, hangup, button or status")
	}
	s, err := readGroups(ctx, dir)
	if err != nil {
		return err
	}
	g, err := chooseGroup(s, *ref)
	if err != nil {
		return err
	}
	if g.Call == nil {
		return fmt.Errorf("this connection uses room mode")
	}
	if *asJSON {
		b, _ := json.Marshal(g.Call)
		fmt.Println(string(b))
	} else {
		fmt.Printf("Call: %s (%s)\n", g.Call.State, cleanText(g.Name))
	}
	return nil
}
