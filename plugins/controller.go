package plugins

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k0ngk0ng/wire-tools/api"
)

type talkConfiguration struct {
	Configured bool   `json:"configured"`
	Mode       string `json:"mode"`
	Input      string `json:"input"`
	Output     string `json:"output"`
	Active     string `json:"active_group"`
	Groups     []struct {
		ID   string `json:"id"`
		Mode string `json:"mode"`
	} `json:"groups"`
}
type talkSnapshot struct {
	Mode       string  `json:"mode"`
	OutputGain float64 `json:"output_gain_db"`
	Call       *struct {
		State string `json:"state"`
	} `json:"call"`
}

type controller struct {
	mu                       sync.Mutex
	status                   Status
	ctx                      context.Context
	cancel                   context.CancelFunc
	done                     chan struct{}
	client                   *api.Client
	name                     string
	direct                   atomic.Bool
	configured               bool
	ids                      []string
	driverIDs                string
	driver                   hardware
	retry                    time.Time
	hardwareError, talkError string
	call                     string
	blinkSince               time.Time
	pressed, armed           map[string]bool
	displaySent              bool
	displayVisible           bool
	sequence                 uint64
}

func newController(ctx context.Context, home, name string) *controller {
	ctx, cancel := context.WithCancel(ctx)
	c := &controller{ctx: ctx, cancel: cancel, done: make(chan struct{}), client: api.New(home), name: name, status: defaultStatus(name), pressed: map[string]bool{}, armed: map[string]bool{}}
	c.status.Enabled = true
	c.status.Running = true
	go c.run()
	return c
}

func (c *controller) update(f func(*Status)) { c.mu.Lock(); defer c.mu.Unlock(); f(&c.status) }
func (c *controller) snapshot() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.status
	if s.Display != nil {
		d := *s.Display
		s.Display = &d
	}
	if s.Buttons != nil {
		s.Buttons = map[string]bool{}
		for k, v := range c.status.Buttons {
			s.Buttons[k] = v
		}
	}
	return s
}
func (c *controller) close() { c.cancel(); <-c.done }
func (c *controller) request(method, path string, body, result any) error {
	ctx, cancel := context.WithTimeout(c.ctx, 1500*time.Millisecond)
	defer cancel()
	return c.client.Request(ctx, method, "/v1/talk"+path, body, result)
}
func (c *controller) errorStatus() {
	err := c.hardwareError
	if err == "" {
		err = c.talkError
	}
	c.update(func(s *Status) { s.Error = err })
}

func (c *controller) poll() {
	var cfg talkConfiguration
	if err := c.request("GET", "/config", nil, &cfg); err != nil {
		c.talkError = err.Error()
		c.direct.Store(false)
		c.setCall("idle")
		c.errorStatus()
		return
	}
	c.configured = cfg.Configured
	c.ids = []string{cfg.Input, cfg.Output}
	mode := cfg.Mode
	for _, g := range cfg.Groups {
		if g.ID == cfg.Active {
			mode = g.Mode
			break
		}
	}
	var current talkSnapshot
	if err := c.request("GET", "/status", nil, &current); err != nil {
		c.talkError = err.Error()
		c.direct.Store(false)
		c.setCall("idle")
		c.errorStatus()
		return
	}
	if current.Mode != "" {
		mode = current.Mode
	}
	c.direct.Store(cfg.Configured && mode == "direct")
	c.talkError = ""
	if !cfg.Configured {
		c.talkError = "Talk is not configured"
	} else if mode != "direct" {
		c.talkError = "select a direct Talk connection to use call buttons"
	}
	state := "idle"
	if current.Call != nil {
		state = current.Call.State
	}
	c.setCall(state)
	c.errorStatus()
}

func (c *controller) setCall(state string) {
	if c.call != state {
		c.call = state
		c.blinkSince = time.Now()
	}
	c.update(func(s *Status) {
		s.CallState = state
		if s.Display != nil {
			s.Display.Blinking = state == "dialing" || state == "ringing"
		}
	})
}

func (c *controller) startDriver() {
	if c.ctx.Err() != nil || c.driver != nil || time.Now().Before(c.retry) {
		return
	}
	d, err := openHardware(c.ctx, c.name, c.ids, c.direct.Load)
	if err != nil {
		c.hardwareError = err.Error()
		c.retry = time.Now().Add(time.Second)
		c.errorStatus()
		return
	}
	c.driver = d
	c.driverIDs = strings.Join(c.ids, "\x00")
	c.displaySent = false
}

func (c *controller) driverEnded(err error) {
	if c.driver != nil {
		c.driver.Close()
		c.driver = nil
	}
	if err != nil {
		c.hardwareError = err.Error()
	} else {
		c.hardwareError = "hardware driver stopped"
	}
	c.update(func(s *Status) {
		s.Online = false
		if s.Display != nil {
			s.Display.Visible = false
		}
	})
	c.retry = time.Now().Add(time.Second)
	c.errorStatus()
}

func (c *controller) render() {
	if c.name != "sakshat" || c.driver == nil {
		return
	}
	visible := c.call == "active"
	if c.call == "dialing" || c.call == "ringing" {
		visible = int(time.Since(c.blinkSince)/(500*time.Millisecond))%2 == 0
	}
	if c.displaySent && visible == c.displayVisible {
		return
	}
	c.sequence++
	if err := c.driver.Display(c.sequence, "6702", visible); err != nil {
		c.hardwareError = err.Error()
		c.errorStatus()
		return
	}
	c.displaySent = true
	c.displayVisible = visible
}

func (c *controller) action(action string) {
	c.poll()
	if !c.direct.Load() {
		return
	}
	var err error
	switch action {
	case "volume-up", "volume-down":
		var s talkSnapshot
		if err = c.request("GET", "/status", nil, &s); err == nil {
			delta := 2.0
			if action == "volume-down" {
				delta = -2
			}
			db := max(-60, min(24, s.OutputGain+delta))
			err = c.request("POST", "/groups", map[string]any{"action": "output-gain", "gain_db": db}, nil)
		}
	default:
		err = c.request("POST", "/call", map[string]string{"action": action}, nil)
	}
	if err != nil {
		c.talkError = err.Error()
		c.errorStatus()
		return
	}
	c.update(func(s *Status) { s.LastAction = action; s.Actions++ })
	c.poll()
	c.render()
}

func (c *controller) buttons(values map[string]bool, initial bool, at time.Time) {
	fresh := map[string]bool{}
	for _, key := range []string{"s1", "s2"} {
		value := values[key]
		if initial {
			c.pressed[key] = value
			c.armed[key] = !value
			continue
		}
		if !value {
			c.armed[key] = true
		}
		fresh[key] = value && !c.pressed[key] && c.armed[key]
		if fresh[key] {
			c.armed[key] = false
		}
		c.pressed[key] = value
	}
	c.update(func(s *Status) { s.Buttons = map[string]bool{"s1": c.pressed["s1"], "s2": c.pressed["s2"]} })
	if initial || (!at.IsZero() && time.Since(at) > time.Second) {
		return
	}
	c.poll()
	if !c.direct.Load() {
		return
	}
	// A simultaneous hangup press wins over dial/answer.
	if fresh["s2"] {
		if c.call != "idle" {
			c.action("hangup")
		}
		return
	}
	if fresh["s1"] {
		switch c.call {
		case "idle":
			c.action("dial")
		case "ringing":
			c.action("answer")
		}
	}
}

func (c *controller) event(e hardwareEvent) {
	switch e.Kind {
	case "ready":
		c.hardwareError = ""
		c.update(func(s *Status) { s.Online = true; s.Device = e.Device; s.SDKVersion = e.SDKVersion })
		c.buttons(e.Buttons, true, e.At)
		c.displaySent = false
		c.errorStatus()
		c.render()
	case "status":
		c.hardwareError = e.Error
		c.update(func(s *Status) { s.Online = e.Online; s.Device = e.Device })
		c.errorStatus()
	case "error":
		c.hardwareError = e.Error
		c.update(func(s *Status) { s.Online = false })
		c.errorStatus()
	case "display":
		c.update(func(s *Status) {
			if s.Display != nil {
				s.Display.Visible = e.Visible
			}
		})
	case "buttons":
		c.buttons(e.Buttons, false, e.At)
	case "action":
		if time.Since(e.At) <= time.Second {
			c.action(e.Action)
		}
	}
}

func (c *controller) run() {
	defer close(c.done)
	defer func() {
		if c.driver != nil {
			c.driver.Close()
		}
		c.update(func(s *Status) {
			s.Running = false
			s.Online = false
			if s.Display != nil {
				s.Display.Visible = false
			}
		})
	}()
	c.poll()
	c.startDriver()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		var events <-chan hardwareEvent
		var done <-chan error
		if c.driver != nil {
			events = c.driver.Events()
			done = c.driver.Done()
		}
		select {
		case <-c.ctx.Done():
			return
		case e := <-events:
			c.event(e)
		case err := <-done:
			c.driverEnded(err)
		case <-tick.C:
			c.poll()
			if c.name == "spacetouch" && c.driver != nil && c.driverIDs != strings.Join(c.ids, "\x00") {
				c.driver.Close()
				c.driver = nil
			}
			c.startDriver()
			c.render()
		}
	}
}

func driverError(err error, diagnostic string) error {
	if diagnostic != "" {
		return fmt.Errorf("hardware driver: %s", diagnostic)
	}
	if err != nil {
		return fmt.Errorf("hardware driver: %w", err)
	}
	return fmt.Errorf("hardware driver exited")
}
