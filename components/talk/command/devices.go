package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/audio"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
)

func setDevicesCommand(ctx context.Context, dir string, args []string) error {
	fs := flag.NewFlagSet("devices set", flag.ContinueOnError)
	input := fs.String("input", "", "microphone device ID, or none")
	output := fs.String("output", "", "call output device ID")
	ring := fs.String("ring-output", "", "incoming ringtone device ID; empty shares call output")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	change := map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "input":
			change["input"] = *input
		case "output":
			change["output"] = *output
		case "ring-output":
			change["ring_output"] = *ring
		}
	})
	if fs.NArg() != 0 || len(change) == 0 {
		return errors.New("usage: devices set [--input ID] [--output ID] [--ring-output ID]")
	}
	body, err := json.Marshal(change)
	if err != nil {
		return err
	}
	if _, err = control.RequestBody(ctx, dir, "PATCH", "/config", body); err != nil {
		return err
	}
	fmt.Println("Audio devices saved. Use wirectl talk join to connect.")
	return nil
}

func devicesCommand(args []string) error {
	fs := flag.NewFlagSet("devices", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON for scripts instead of a table")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: wirectl talk devices [--json]\n       wirectl talk devices set [--input ID] [--output ID] [--ring-output ID]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: devices [--json]")
	}
	ds, err := audio.List()
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(ds)
	}
	if len(ds) == 0 {
		fmt.Println("No audio devices found. Connect a microphone and audio output to use talk.")
		return nil
	}
	// Device names come from drivers. Keep control characters out of the table
	// while preserving the original strings in machine-readable JSON.
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "TYPE\tDEFAULT\tNAME\tID")
	hasInput, hasOutput := false, false
	for _, d := range ds {
		mark := "-"
		if d.Default {
			mark = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", cleanText(d.Kind), mark, cleanText(d.Name), cleanText(d.ID))
		hasInput = hasInput || d.Kind == "input"
		hasOutput = hasOutput || d.Kind == "output"
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if !hasInput {
		fmt.Println("\nNo input devices found. Connect a microphone to use talk.")
	}
	if !hasOutput {
		fmt.Println("\nNo output devices found. Connect headphones or speakers to use talk.")
	}
	return nil
}

func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
