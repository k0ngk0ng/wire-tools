package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/k0ngk0ng/wire-tools/api"
	"github.com/k0ngk0ng/wire-tools/plugins"
)

func pluginCommand(ctx context.Context, client *api.Client, args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" || args[0] == "-h" {
		fmt.Println("Usage: wirectl plugin list [--json] | status NAME [--json] | enable NAME | disable NAME")
		fmt.Println("Optional hardware plugins: sakshat (Raspberry Pi SAKS), spacetouch (USB handset).")
		return nil
	}
	action := args[0]
	name := ""
	tail := args[1:]
	switch action {
	case "list":
	case "status", "enable", "disable":
		if len(tail) == 0 {
			return errors.New("provide the plugin name: sakshat or spacetouch")
		}
		name = tail[0]
		tail = tail[1:]
		if name != "sakshat" && name != "spacetouch" {
			return fmt.Errorf("unknown plugin %q", name)
		}
	default:
		return errors.New("choose plugin list, status, enable or disable")
	}
	fs := flag.NewFlagSet("plugin "+action, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON")
	if err := fs.Parse(tail); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected plugin arguments")
	}
	if action == "list" {
		var rows []plugins.Status
		if err := client.Request(ctx, "GET", "/v1/plugins", nil, &rows); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(rows)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "PLUGIN\tENABLED\tONLINE\tDESCRIPTION")
		for _, s := range rows {
			fmt.Fprintf(w, "%s\t%t\t%t\t%s\n", s.Name, s.Enabled, s.Online, s.Description)
		}
		return w.Flush()
	}
	method := "GET"
	var body any
	if action == "enable" || action == "disable" {
		method = "POST"
		body = map[string]bool{"enabled": action == "enable"}
	}
	var s plugins.Status
	if err := client.Request(ctx, method, "/v1/plugins/"+name, body, &s); err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(s)
	}
	fmt.Printf("%s · enabled: %t · online: %t · call: %s\n", s.Name, s.Enabled, s.Online, s.CallState)
	if s.Display != nil && s.Display.Visible {
		fmt.Printf("Display: %s\n", s.Display.Text)
	}
	if s.Error != "" {
		fmt.Println(s.Error)
	}
	return nil
}
