// Package handset reads only the supported USB handset's consumer controls.
package handset

import (
	"context"
	"sync/atomic"
)

type Status struct {
	Device string `json:"device"`
	Online bool   `json:"online"`
	Error  string `json:"error,omitempty"`
}

type Listener struct {
	cancel   context.CancelFunc
	done     chan struct{}
	state    atomic.Pointer[Status]
	pressed  map[uint32]bool
	action   func(string)
	enabled  func() bool
	device   string
	audioIDs []string
}

func Start(ctx context.Context, enabled func() bool, action func(string), audioIDs ...string) *Listener {
	ctx, cancel := context.WithCancel(ctx)
	l := &Listener{cancel: cancel, done: make(chan struct{}), pressed: map[uint32]bool{}, action: action, enabled: enabled, device: "AB17X USB Audio"}
	l.audioIDs = append([]string(nil), audioIDs...)
	l.update(false, "waiting for device")
	go func() { defer close(l.done); l.run(ctx) }()
	return l
}
func (l *Listener) update(online bool, message string) {
	l.state.Store(&Status{Device: l.device, Online: online, Error: message})
}
func (l *Listener) Status() Status { return *l.state.Load() }
func (l *Listener) Close()         { l.cancel(); <-l.done }

func (l *Listener) input(page, usage uint32, value int) {
	if page != 12 {
		return
	}
	actions := map[uint32]string{205: "button", 233: "volume-up", 234: "volume-down"}
	action, ok := actions[usage]
	if !ok {
		return
	}
	down := value != 0
	previous := l.pressed[usage]
	l.pressed[usage] = down
	if down && !previous {
		l.action(action)
	}
}
