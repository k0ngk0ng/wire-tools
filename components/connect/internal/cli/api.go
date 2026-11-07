package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	localapi "github.com/k0ngk0ng/wire-tools/api"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/client"
)

type resourceConnection struct {
	Name        string        `json:"name"`
	Server      string        `json:"server"`
	PairingCode string        `json:"pairing_code"`
	State       string        `json:"state"`
	Error       string        `json:"error"`
	Status      client.Status `json:"status"`
}

// RunAPI preserves CLI parsing and presentation while all connection
// operations go through the runtime's resource API. Internal helper/server
// invocations deliberately retain their native adapter path.
func RunAPI(ctx context.Context, args []string, version string, in io.Reader, out, errOut io.Writer) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	a := app{in: in, out: out, errOut: errOut, version: version}
	var err error
	switch args[0] {
	case "status", "list":
		err = a.resourceStatus(ctx, args[1:])
	case "stop":
		err = a.resourceStop(ctx, args[1:], false)
	case "delete", "remove":
		err = a.resourceStop(ctx, args[1:], true)
	case "resume":
		err = a.resourceConnect(ctx, args[1:], true)
	case "login":
		err = a.resourceLogin(ctx, args[1:])
	case "doctor":
		err = a.resourceDoctor(ctx, args[1:])
	case "setup", "serve", "completion", "version", "--version", "help", "--help", "-h":
		return false, nil
	default:
		if strings.HasPrefix(args[0], "__") {
			return false, nil
		}
		err = a.resourceConnect(ctx, args, false)
	}
	if errors.Is(err, flag.ErrHelp) {
		err = nil
	}
	return true, err
}

func validateResourceDir(c *common) error {
	if c.dir == "" {
		return nil
	}
	expected := os.Getenv("WIRECTL_CONNECT_HOME")
	if expected == "" {
		home, err := localapi.DefaultHome()
		if err != nil {
			return err
		}
		expected = filepath.Join(home, "connect")
	}
	actual, err := filepath.Abs(c.dir)
	if err != nil {
		return err
	}
	expected, err = filepath.Abs(expected)
	if err != nil {
		return err
	}
	if actual != expected {
		return errors.New("--state-dir must match the selected runtime; set WIRECTL_HOME to use a separate runtime")
	}
	return nil
}
func resourcePath(name string) string { return "/v1/connect/connections/" + url.PathEscape(name) }

func (a app) resourceStatus(ctx context.Context, args []string) error {
	f, c, err := a.flags("status")
	if err != nil {
		return err
	}
	watch := f.Bool("watch", false, "refresh once per second")
	asJSON := f.Bool("json", false, "print JSON")
	all := f.Bool("all", false, "show all saved connections")
	if err := parse(f, args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("status takes no positional arguments")
	}
	if err := validateResourceDir(c); err != nil {
		return err
	}
	named := false
	f.Visit(func(fl *flag.Flag) {
		if fl.Name == "name" {
			named = true
		}
	})
	if *all && named {
		return errors.New("use either --all or --name")
	}
	showAll := *all || (!named && !*asJSON)
	api := localapi.New("")
	for {
		var response struct {
			Connections []resourceConnection `json:"connections"`
		}
		if err := api.Request(ctx, "GET", "/v1/connect/connections", nil, &response); err != nil {
			return err
		}
		rows := []connectionStatus{}
		for _, row := range response.Connections {
			if !showAll && row.Name != c.name {
				continue
			}
			status := row.Status
			if status.Mode == "" {
				status.Mode = row.State
			}
			rows = append(rows, connectionStatus{Name: row.Name, Server: row.Server, Status: status, Error: row.Error})
		}
		if !showAll && len(rows) == 0 {
			return fmt.Errorf("no saved connection named %s", c.name)
		}
		if *asJSON {
			var value any = rows
			if !showAll {
				value = rows[0].Status
			}
			if err := json.NewEncoder(a.out).Encode(value); err != nil {
				return err
			}
		} else {
			printConnections(a.out, rows, statusColor(a.out))
		}
		if !*watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
func (a app) resourceStop(ctx context.Context, args []string, forget bool) error {
	f, c, err := a.flags("stop")
	if err != nil {
		return err
	}
	_ = f.Bool("uninstall", false, "legacy option; the runtime owns sessions")
	if err := parse(f, args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return errors.New("stop/delete takes no positional arguments")
	}
	if err := validateResourceDir(c); err != nil {
		return err
	}
	path := resourcePath(c.name)
	if forget {
		path += "?forget=true"
	}
	if err := localapi.New("").Request(ctx, "DELETE", path, nil, nil); err != nil {
		return err
	}
	if forget {
		fmt.Fprintf(a.out, "Deleted connection %s and its saved pair.\n", c.name)
	} else {
		fmt.Fprintf(a.out, "Stopped connection %s. Saved pair retained.\n", c.name)
	}
	return nil
}
func (a app) resourceLogin(ctx context.Context, args []string) error {
	f, c, err := a.flags("login")
	if err != nil {
		return err
	}
	pin := f.String("pin", "", "SHA-256 certificate fingerprint")
	if err := parse(f, args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return errors.New("usage: wirectl connect login <server> [--pin SHA256]")
	}
	if err := validateResourceDir(c); err != nil {
		return err
	}
	fmt.Fprint(a.errOut, "Server enrollment token: ")
	token, err := readSecret(a.in)
	fmt.Fprintln(a.errOut)
	if err != nil {
		return err
	}
	if token == "" {
		return errors.New("empty enrollment token")
	}
	var response struct {
		Server string `json:"server"`
	}
	err = localapi.New("").Request(ctx, "POST", "/v1/connect/credentials", map[string]string{"server": f.Arg(0), "token": token, "certificate_sha256": *pin}, &response)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, "Authorized", response.Server)
	return nil
}
func (a app) resourceDoctor(ctx context.Context, args []string) error {
	f, c, err := a.flags("doctor")
	if err != nil {
		return err
	}
	if err := parse(f, args); err != nil {
		return err
	}
	if f.NArg() > 1 {
		return errors.New("doctor accepts at most one server")
	}
	if err := validateResourceDir(c); err != nil {
		return err
	}
	path := "/v1/connect/doctor"
	if f.NArg() == 1 {
		path += "?server=" + url.QueryEscape(f.Arg(0))
	}
	var result struct {
		Platform string   `json:"platform"`
		Ready    bool     `json:"ready"`
		Issues   []string `json:"issues"`
	}
	if err := localapi.New("").Request(ctx, "GET", path, nil, &result); err != nil {
		return err
	}
	fmt.Fprintln(a.out, "Platform:", result.Platform)
	for _, issue := range result.Issues {
		fmt.Fprintln(a.out, issue)
	}
	if !result.Ready {
		return errors.New("network prerequisites need attention")
	}
	fmt.Fprintln(a.out, "Tunnel prerequisites: ready")
	return nil
}
func (a app) resourceConnect(ctx context.Context, args []string, resume bool) error {
	f, c, err := a.flags("connect")
	if err != nil {
		return err
	}
	background := f.Bool("background", true, "keep connection in the runtime")
	foreground := f.Bool("foreground", false, "wait until Ctrl+C and stop this connection")
	network := f.String("network", "100.64.0.0/10", "private virtual address range")
	iface := f.String("interface", "", "virtual interface name")
	mtu := f.Int("mtu", 1420, "virtual interface MTU")
	stun := f.String("stun", "", "STUN URL override")
	relay := f.Bool("relay-only", false, "disable UDP direct connectivity")
	_ = f.Bool("verbose", false, "show connection diagnostics")
	code := f.String("code", "", "create a room using this pairing code")
	replace := f.Bool("replace", false, "replace a saved pair after authenticating")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := validateResourceDir(c); err != nil {
		return err
	}
	path := "/v1/connect/connections"
	request := map[string]any{}
	if resume {
		if f.NArg() != 0 {
			return errors.New("resume takes no positional arguments")
		}
		path = resourcePath(c.name) + "/resume"
		f.Visit(func(fl *flag.Flag) {
			switch fl.Name {
			case "interface":
				request["interface"] = *iface
			case "mtu":
				request["mtu"] = *mtu
			case "stun":
				request["stun_url"] = *stun
			case "relay-only":
				request["relay_only"] = *relay
			}
		})
	} else {
		if f.NArg() < 1 || f.NArg() > 2 {
			return errors.New("usage: wirectl connect <server> [code]")
		}
		host := f.NArg() == 1
		if !host {
			if *code != "" {
				return errors.New("use either positional join code or --code for creation")
			}
			*code = f.Arg(1)
		}
		request = map[string]any{"name": c.name, "server": f.Arg(0), "host": host, "code": *code, "network": *network, "interface": *iface, "mtu": *mtu, "stun_url": *stun, "relay_only": *relay, "replace": *replace}
	}
	api := localapi.New("")
	var connection resourceConnection
	if err := api.Request(ctx, "POST", path, request, &connection); err != nil {
		return err
	}
	waitForeground := *foreground || !*background
	if waitForeground {
		// Cancellation can arrive during a status request, before the select
		// below. Always release this explicit foreground connection on exit.
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = api.Request(stopCtx, "DELETE", resourcePath(c.name), nil, nil)
		}()
	}

	lastCode := ""
	for {
		if err := api.Request(ctx, "GET", resourcePath(c.name), nil, &connection); err != nil {
			return err
		}
		if connection.PairingCode != "" && connection.PairingCode != lastCode {
			lastCode = connection.PairingCode
			fmt.Fprintf(a.out, "Pairing code: %s\nWaiting for the other device…\n", lastCode)
		}
		if connection.State == "failed" {
			return errors.New(connection.Error)
		}
		if connection.State == "stopped" {
			return nil
		}
		if connection.State == "running" && !waitForeground {
			fmt.Fprintln(a.out, "Connected in background")
			printConnections(a.out, []connectionStatus{{Name: c.name, Server: connection.Server, Status: connection.Status}}, statusColor(a.out))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
