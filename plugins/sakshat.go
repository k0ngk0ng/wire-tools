package plugins

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed sakshat_driver.py
var sakshatSource string

type diagnosticTail struct {
	mu   sync.Mutex
	text string
}

func (t *diagnosticTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.text += string(p)
	if len(t.text) > 2048 {
		t.text = t.text[len(t.text)-2048:]
	}
	return len(p), nil
}
func (t *diagnosticTail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(t.text)
}

type displayCommand struct {
	Kind    string `json:"kind"`
	Seq     uint64 `json:"seq"`
	Text    string `json:"text"`
	Visible bool   `json:"visible"`
}
type sdkHardware struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	events         chan hardwareEvent
	done           chan error
	exited         chan struct{}
	closing        chan struct{}
	commands       chan displayCommand
	once, failOnce sync.Once
}

func openSAKSHAT(ctx context.Context) (hardware, error) {
	extra, test := sdkTestEnvironment()
	pythonName := "python3"
	if test && os.Getenv("WIRECTL_TEST_SAKSHAT_PYTHON") != "" {
		pythonName = os.Getenv("WIRECTL_TEST_SAKSHAT_PYTHON")
	}
	python, err := exec.LookPath(pythonName)
	if err != nil {
		return nil, err
	}
	args := []string{"-u", "-c", sakshatSource}
	if test {
		args = append(args, "--test-sdk")
	}
	cmd := exec.Command(python, args...)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1")
	cmd.Env = append(cmd.Env, extra...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, err
	}
	tail := &diagnosticTail{}
	cmd.Stderr = tail
	if err = cmd.Start(); err != nil {
		stdin.Close()
		return nil, err
	}
	d := &sdkHardware{cmd: cmd, stdin: stdin, events: make(chan hardwareEvent, 128), done: make(chan error, 1), exited: make(chan struct{}), closing: make(chan struct{}), commands: make(chan displayCommand, 1)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 65536)
		for scanner.Scan() {
			var e hardwareEvent
			if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
				d.fail(driverError(err, "invalid SDK response"))
				return
			}
			e.At = time.Now()
			select {
			case d.events <- e:
			case <-ctx.Done():
				return
			case <-d.closing:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			d.fail(err)
		}
	}()
	go func() { err := cmd.Wait(); close(d.exited); d.fail(driverError(err, tail.last())) }()
	go func() {
		encoder := json.NewEncoder(stdin)
		for {
			select {
			case <-d.closing:
				return
			case command := <-d.commands:
				if err := encoder.Encode(command); err != nil {
					d.fail(err)
					return
				}
			}
		}
	}()
	return d, nil
}

func (d *sdkHardware) fail(err error)               { d.failOnce.Do(func() { d.done <- err }) }
func (d *sdkHardware) Events() <-chan hardwareEvent { return d.events }
func (d *sdkHardware) Done() <-chan error           { return d.done }
func (d *sdkHardware) Display(seq uint64, text string, visible bool) error {
	command := displayCommand{Kind: "display", Seq: seq, Text: text, Visible: visible}
	select {
	case <-d.closing:
		return io.ErrClosedPipe
	default:
	}
	select {
	case d.commands <- command:
	default:
		select {
		case <-d.commands:
		default:
		}
		select {
		case d.commands <- command:
		case <-d.closing:
			return io.ErrClosedPipe
		}
	}
	return nil
}
func (d *sdkHardware) Close() {
	d.once.Do(func() {
		close(d.closing)
		_ = d.stdin.Close()
		select {
		case <-d.exited:
			return
		default:
		}
		_ = d.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-d.exited:
		case <-time.After(2 * time.Second):
			_ = d.cmd.Process.Kill()
			<-d.exited
		}
	})
}
