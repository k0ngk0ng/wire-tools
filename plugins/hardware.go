package plugins

import (
	"context"
	"time"
)

type hardwareEvent struct {
	Kind       string          `json:"kind"`
	Online     bool            `json:"online,omitempty"`
	Device     string          `json:"device,omitempty"`
	SDKVersion string          `json:"sdk_version,omitempty"`
	Buttons    map[string]bool `json:"buttons,omitempty"`
	Action     string          `json:"action,omitempty"`
	Error      string          `json:"error,omitempty"`
	Seq        uint64          `json:"seq,omitempty"`
	Visible    bool            `json:"visible,omitempty"`
	At         time.Time       `json:"-"`
}

type hardware interface {
	Events() <-chan hardwareEvent
	Done() <-chan error
	Display(seq uint64, text string, visible bool) error
	Close()
}

func openHardware(ctx context.Context, name string, ids []string, allowed func() bool) (hardware, error) {
	if name == "sakshat" {
		return openSAKSHAT(ctx)
	}
	return openSpacetouch(ctx, ids, allowed), nil
}
