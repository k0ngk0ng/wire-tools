package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/k0ngk0ng/wire-tools/internal/app"
	"github.com/k0ngk0ng/wire-tools/internal/host"
	"github.com/k0ngk0ng/wire-tools/internal/installpath"
)

var version = "dev"

func main() {
	// Updated bundles live in immutable generation directories. The executable
	// selects dependencies from its own version before any feature is loaded.
	if self, err := os.Executable(); err == nil {
		if p, err := installpath.Resolve(self); err == nil {
			self = p
		}
		root := filepath.Dir(self)
		if filepath.Base(root) == "bin" {
			root = filepath.Dir(root)
		}
		bundle := filepath.Join(root, ".wirectl", "versions", version)
		if st, err := os.Stat(bundle); err == nil && st.IsDir() {
			_ = os.Setenv("WIRECTL_BUNDLE_DIR", bundle)
		}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	err := app.Run(ctx, os.Args[1:], version)
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, flag.ErrHelp) {
		return
	}
	var command *host.CommandError
	if errors.As(err, &command) {
		os.Exit(command.Code)
	}
	fmt.Fprintln(os.Stderr, "wirectl:", err)
	os.Exit(1)
}
