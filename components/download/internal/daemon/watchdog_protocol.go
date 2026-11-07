package daemon

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/private"
)

// stopEngineProtocol stops the real core through its authenticated control
// protocol, even when the process handle belongs to a package-manager shim.
// Only the protected config path is present in argv; credentials stay local.
func stopEngineProtocol(spec engineSpec) bool {
	var path string
	for i, arg := range spec.Args {
		if value, ok := strings.CutPrefix(arg, "--conf-path="); ok {
			path = value
			break
		}
		if arg == "--conf-path" && i+1 < len(spec.Args) {
			path = spec.Args[i+1]
			break
		}
	}
	if path == "" || private.Check(path) != nil {
		return false
	}
	stat, err := os.Stat(path)
	if err != nil || stat.Size() > 1<<20 {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var secret string
	port := 0
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "rpc-listen-port":
			port, _ = strconv.Atoi(strings.TrimSpace(value))
		case "rpc-secret":
			secret = strings.TrimSpace(value)
		}
	}
	if port < 1 || port > 65535 || len(secret) < 32 || strings.ContainsAny(secret, "\r\n\x00") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	aria := engine.NewAria2(port, secret)
	aria.Client = &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	// forceShutdown aborts active transfers promptly and still runs aria2's
	// shutdown/session persistence. Windows does not support POSIX SIGTERM;
	// waiting on that signal would let an orphan finish and prune its session.
	if err = freezeAria2BeforeShutdown(ctx, aria, filepath.Dir(path)); err != nil {
		return false
	}
	saved := aria.SaveSession(ctx) == nil
	stopped := aria.Call(ctx, "forceShutdown", nil, nil) == nil
	return saved && stopped
}
