package command

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/audio"
)

func devicesCommand(args []string) error {
	fs := flag.NewFlagSet("devices", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "output JSON for scripts instead of a table")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: wirectl talk devices [--json]")
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
