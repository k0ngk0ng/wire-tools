// Package cli provides shared routing and terminal frontend utilities.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

type Command struct {
	Summary string
	Run     func(context.Context, []string) error
}
type App struct {
	Name        string
	Description string
	Commands    map[string]Command
	Default     func(context.Context, []string) error
	// PluginPrefix enables PATH dispatch; only the root app should set it.
	PluginPrefix string
	Out          io.Writer
}

var commandName = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func (a App) Run(ctx context.Context, args []string) error {
	if a.Out == nil {
		a.Out = os.Stdout
	}
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(a.Out, "%s — %s\n\nUsage: %s <command> [arguments]\n", a.Name, a.Description, a.Name)
		names := make([]string, 0, len(a.Commands))
		for name := range a.Commands {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(a.Out, "  %-12s %s\n", name, a.Commands[name].Summary)
		}
		if a.PluginPrefix != "" {
			for _, name := range plugins(a.PluginPrefix) {
				if _, known := a.Commands[name]; !known {
					fmt.Fprintf(a.Out, "  %-12s Installed plugin\n", name)
				}
			}
			fmt.Fprintf(a.Out, "\nInstalled commands are discovered as %s<command> on PATH.\n", a.PluginPrefix)
		}
		return nil
	}
	if cmd, ok := a.Commands[args[0]]; ok {
		return cmd.Run(ctx, args[1:])
	}
	if a.PluginPrefix != "" && commandName.MatchString(args[0]) {
		path := resolvePlugin(a.PluginPrefix, args[0])
		if path == "" {
			return fmt.Errorf("command %q is not installed; install %s%s and place it on PATH", args[0], a.PluginPrefix, args[0])
		}
		cmd := exec.CommandContext(ctx, path, args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if a.Default != nil {
		return a.Default(ctx, args)
	}
	return fmt.Errorf("unknown command %q (use %s --help)", args[0], a.Name)
}

func plugins(prefix string) []string {
	dirs := filepath.SplitList(os.Getenv("PATH"))
	if self, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		dirs = append(dirs, filepath.Dir(self))
	}
	found := map[string]bool{}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			name, ok := pluginCommandName(prefix, entry.Name())
			if !ok {
				continue
			}
			if isPluginExecutable(filepath.Join(dir, entry.Name())) {
				found[name] = true
			}
		}
	}
	names := []string{}
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolvePlugin resolves a command plugin by giving the host's directory
// precedence over PATH. Windows executables conventionally carry an .exe
// suffix, while Unix relies on executable permission bits.
func resolvePlugin(prefix, command string) string {
	self, err := os.Executable()
	if err != nil {
		self = ""
	}
	return resolvePluginFrom(prefix, command, self)
}

// resolvePluginFrom is split out so resolution order can be tested without
// having to place a fixture beside the running test binary.
func resolvePluginFrom(prefix, command, self string) string {
	plugin := prefix + command
	candidates := pluginCandidates(plugin)
	if self != "" {
		self = absolutePath(self)
		if resolved, err := filepath.EvalSymlinks(self); err == nil {
			self = resolved
		}
		for _, candidate := range candidates {
			path := filepath.Join(filepath.Dir(self), candidate)
			if isPluginExecutable(path) {
				return absolutePath(path)
			}
		}
	}
	for _, candidate := range candidates {
		path, err := exec.LookPath(candidate)
		if err != nil || !isPluginExecutable(path) {
			continue
		}
		return absolutePath(path)
	}
	return ""
}

func pluginCandidates(plugin string) []string {
	if runtime.GOOS == "windows" {
		return []string{plugin + ".exe", plugin}
	}
	// Unix command names are exact and executable permission is part of the
	// plugin contract.  Do not silently make a Windows-named file a Unix
	// plugin: that would change discovery behavior for existing installations.
	return []string{plugin}
}

func pluginCommandName(prefix, filename string) (string, bool) {
	name := strings.TrimPrefix(filename, prefix)
	if name == filename {
		return "", false
	}
	if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(name), ".exe") {
		name = name[:len(name)-len(filepath.Ext(name))]
	}
	if !commandName.MatchString(name) {
		return "", false
	}
	return name, true
}

func isPluginExecutable(path string) bool {
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Ext(path), ".exe")
	}
	return st.Mode()&0111 != 0
}

func absolutePath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		return absolute
	}
	return path
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(*exec.ExitError); ok && e.ExitCode() > 0 {
		return e.ExitCode()
	}
	return 1
}
