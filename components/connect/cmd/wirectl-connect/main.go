package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/k0ngk0ng/wire-tools/components/connect/internal/cli"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/service"
)

var version = "dev"

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "__connect" {
		args = args[1:]
	}
	run := func(ctx context.Context) error {
		return cli.Run(ctx, args, version, os.Stdin, os.Stdout, os.Stderr)
	}
	if handled, err := service.Run(run); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
}
