package command

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/k0ngk0ng/wire-tools/components/talk/internal/control"
)

func TestMuteDirectionsAreIndependent(t *testing.T) {
	dir := t.TempDir()
	var input, output atomic.Bool
	server, err := control.Start(dir, func() any { return nil }, func() {}, input.Store, output.Store)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	for _, tc := range []struct {
		muted         bool
		args          []string
		input, output bool
	}{
		{true, nil, true, false},
		{true, []string{"output"}, true, true},
		{false, []string{"input"}, false, true},
		{false, []string{"output"}, false, false},
	} {
		if err := muteCommand(context.Background(), dir, tc.muted, tc.args); err != nil {
			t.Fatal(err)
		}
		if input.Load() != tc.input || output.Load() != tc.output {
			t.Fatalf("wrong state after %v: input=%v output=%v", tc.args, input.Load(), output.Load())
		}
	}
	for _, args := range [][]string{{"speaker"}, {"input", "output"}} {
		if err := muteCommand(context.Background(), dir, true, args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if input.Load() || output.Load() {
		t.Fatal("invalid command changed mute state")
	}
}
