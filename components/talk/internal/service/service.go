// Package service registers per-user native audio services, never system/root services.
package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/installpath"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

type Plan struct {
	Path        string
	Content     string
	Start, Stop [][]string
}

func quoted(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
}
func xmlText(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }

// Build is pure so each platform's service definition can be checked on any OS.
func Build(goos, home, configDir, uid, exe, state string) (Plan, error) {
	for _, v := range []string{home, configDir, uid, exe, state} {
		if strings.ContainsAny(v, "\r\n\x00") {
			return Plan{}, fmt.Errorf("service paths may not contain control characters")
		}
	}
	sum := sha256.Sum256([]byte(state))
	id := fmt.Sprintf("io.github.k0ngk0ng.talk.%x", sum[:6])
	p := Plan{}
	switch goos {
	case "linux":
		name := id + ".service"
		p.Path = filepath.Join(configDir, "systemd", "user", name)
		p.Content = "[Unit]\nDescription=Wire Talk microphone and speaker\nAfter=network-online.target sound.target\n\n[Service]\nType=simple\nExecStart=" + quoted(exe) + " __serve --state-dir " + quoted(state) + "\nRestart=always\nRestartSec=3\nTimeoutStopSec=10\n\n[Install]\nWantedBy=default.target\n"
		p.Start = [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", "--now", name}}
		p.Stop = [][]string{{"systemctl", "--user", "disable", "--now", name}}
	case "darwin":
		p.Path = filepath.Join(home, "Library", "LaunchAgents", id+".plist")
		p.Content = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>Label</key><string>` + id + `</string><key>ProgramArguments</key><array><string>` + xmlText(exe) + `</string><string>__serve</string><string>--state-dir</string><string>` + xmlText(state) + `</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>3</integer></dict></plist>`
		domain := "gui/" + uid
		p.Start = [][]string{{"launchctl", "bootstrap", domain, p.Path}}
		p.Stop = [][]string{{"launchctl", "bootout", domain + "/" + id}}
	case "windows":
		p.Path = filepath.Join(state, "service.xml")
		// InteractiveToken is essential: a session-0 service cannot use the logged-in
		// user's microphone. Scheduler handles crash recovery after login.
		args := `__serve --state-dir "` + state + `"`
		if strings.Contains(state, `"`) {
			return Plan{}, fmt.Errorf("invalid Windows path")
		}
		p.Content = `<?xml version="1.0" encoding="UTF-8"?><Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Triggers><LogonTrigger><Enabled>true</Enabled><UserId>` + xmlText(uid) + `</UserId></LogonTrigger></Triggers><Principals><Principal id="user"><UserId>` + xmlText(uid) + `</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals><Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit><RestartOnFailure><Interval>PT1M</Interval><Count>999</Count></RestartOnFailure></Settings><Actions Context="user"><Exec><Command>` + xmlText(exe) + `</Command><Arguments>` + xmlText(args) + `</Arguments></Exec></Actions></Task>`
		p.Start = [][]string{{"schtasks", "/Create", "/TN", id, "/XML", p.Path, "/F"}, {"schtasks", "/Run", "/TN", id}}
		p.Stop = [][]string{{"schtasks", "/Delete", "/TN", id, "/F"}}
	default:
		return Plan{}, fmt.Errorf("unsupported service platform %s", goos)
	}
	return p, nil
}
func Current(state string) (Plan, error) {
	u, err := user.Current()
	if err != nil {
		return Plan{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Plan{}, err
	}
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Plan{}, err
	}
	exe, err := os.Executable()
	if err != nil {
		return Plan{}, err
	}
	exe, err = installpath.Resolve(exe)
	if err != nil {
		return Plan{}, err
	}
	return Build(runtime.GOOS, home, cfg, u.Uid, exe, state)
}
func run(commands [][]string) error {
	for _, a := range commands {
		out, err := exec.Command(a[0], a[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", a[0], err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
func Install(state string) error {
	p, err := Current(state)
	if err != nil {
		return err
	}
	if _, err = os.Stat(p.Path); err == nil {
		return fmt.Errorf("service already installed; use daemon stop before installing again")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p.Path), 0700); err != nil {
		return err
	}
	if err = os.WriteFile(p.Path, encodeDefinition(runtime.GOOS, p.Content), 0600); err != nil {
		return err
	}
	if err = run(p.Start); err != nil {
		return fmt.Errorf("service registration failed (definition kept at %s): %w", p.Path, err)
	}
	return nil
}
func Remove(state string) (bool, error) {
	p, err := Current(state)
	if err != nil {
		return false, err
	}
	if _, err = os.Stat(p.Path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err = run(p.Stop); err != nil {
		return true, err
	}
	return true, os.Remove(p.Path)
}

// Task Scheduler's XML file loader expects UTF-16 even though the logical XML
// is generated as UTF-8 for portable validation and escaping.
func encodeDefinition(goos, content string) []byte {
	if goos != "windows" {
		return []byte(content)
	}
	content = strings.Replace(content, `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	units := utf16.Encode([]rune(content))
	out := make([]byte, 2+len(units)*2)
	out[0] = 0xff
	out[1] = 0xfe
	for i, u := range units {
		binary.LittleEndian.PutUint16(out[2+i*2:], u)
	}
	return out
}
