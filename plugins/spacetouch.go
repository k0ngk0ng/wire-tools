package plugins

import (
	"context"
	"sync"
	"time"

	"github.com/k0ngk0ng/wire-tools/internal/handset"
)

type usbHardware struct {
	cancel context.CancelFunc
	events chan hardwareEvent
	done   chan error
	exited chan struct{}
	once   sync.Once
}

func openSpacetouch(ctx context.Context, ids []string, allowed func() bool) *usbHardware {
	ctx, cancel := context.WithCancel(ctx)
	d := &usbHardware{cancel: cancel, events: make(chan hardwareEvent, 128), done: make(chan error, 1), exited: make(chan struct{})}
	l := handset.Start(ctx, allowed, func(action string) {
		select {
		case d.events <- hardwareEvent{Kind: "action", Action: action, At: time.Now()}:
		case <-ctx.Done():
		}
	}, ids...)
	go func() {
		defer close(d.exited)
		defer l.Close()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		var last handset.Status
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				s := l.Status()
				if s == last {
					continue
				}
				last = s
				select {
				case d.events <- hardwareEvent{Kind: "status", Online: s.Online, Device: s.Device, Error: s.Error, At: time.Now()}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return d
}

func (d *usbHardware) Events() <-chan hardwareEvent       { return d.events }
func (d *usbHardware) Done() <-chan error                 { return d.done }
func (d *usbHardware) Display(uint64, string, bool) error { return nil }
func (d *usbHardware) Close()                             { d.once.Do(d.cancel); <-d.exited }
