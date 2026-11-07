package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("WIRECTL_CLI_EXIT_HELPER") == "23" {
		os.Exit(23)
	}
}

func TestRouting(t *testing.T) {
	var got []string
	app := App{Name: "test", Commands: map[string]Command{"download": {Run: func(ctx context.Context, args []string) error { got = args; return nil }}}}
	if err := app.Run(context.Background(), []string{"download", "magnet:?a=b&c=d"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "magnet:?a=b&c=d" {
		t.Fatal(got)
	}
}
func TestHelpAndUnknown(t *testing.T) {
	var out bytes.Buffer
	app := App{Name: "wirectl", Out: &out, PluginPrefix: "not-installed-"}
	if err := app.Run(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "PATH") {
		t.Fatal(out.String())
	}
	if err := app.Run(context.Background(), []string{"../bad"}); err == nil {
		t.Fatal("invalid plugin name accepted")
	}
}

func TestPluginCommandNamePlatformRules(t *testing.T) {
	exeOK := runtime.GOOS == "windows"
	exeWant := ""
	if exeOK {
		exeWant = "probe"
	}
	tests := []struct {
		filename string
		want     string
		ok       bool
	}{
		{filename: "wirectl-probe", want: "probe", ok: true},
		{filename: "wirectl-probe.exe", want: exeWant, ok: exeOK},
		{filename: "wirectl-probe.EXE", want: exeWant, ok: exeOK},
		{filename: "wirectl-probe.exe.bak", ok: false},
		{filename: "wirectl-probe/extra", ok: false},
	}
	for _, tt := range tests {
		got, ok := pluginCommandName("wirectl-", tt.filename)
		if got != tt.want || ok != tt.ok {
			t.Errorf("pluginCommandName(%q) = (%q, %t), want (%q, %t)", tt.filename, got, ok, tt.want, tt.ok)
		}
	}
}

func TestResolvePluginPrefersHostDirectory(t *testing.T) {
	hostDir := t.TempDir()
	pathDir := t.TempDir()
	host := filepath.Join(hostDir, "wirectl")
	pluginName := "wirectl-probe"
	if runtime.GOOS == "windows" {
		pluginName += ".exe"
	}
	sibling := filepath.Join(hostDir, pluginName)
	pathPlugin := filepath.Join(pathDir, pluginName)
	writePluginFixture(t, sibling)
	writePluginFixture(t, pathPlugin)
	t.Setenv("PATH", pathDir)

	got := resolvePluginFrom("wirectl-", "probe", host)
	if got != sibling {
		t.Fatalf("resolvePluginFrom returned %q, want host sibling %q", got, sibling)
	}
}

func TestResolvePluginFindsNativePluginOnPath(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "wirectl-probe")
	if runtime.GOOS == "windows" {
		plugin += ".exe"
	}
	writePluginFixture(t, plugin)
	t.Setenv("PATH", dir)

	got := resolvePluginFrom("wirectl-", "probe", "")
	if got != plugin {
		t.Fatalf("resolvePluginFrom returned %q, want %q", got, plugin)
	}
}

func TestPluginsListsNativePluginWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	plugin := filepath.Join(dir, "wirectl-probe")
	if runtime.GOOS == "windows" {
		plugin += ".exe"
	}
	writePluginFixture(t, plugin)
	t.Setenv("PATH", dir)

	for _, name := range plugins("wirectl-") {
		if name == "probe" {
			return
		}
	}
	t.Fatalf("plugins did not include probe: %v", plugins("wirectl-"))
}

func TestPluginExitCodeIsPreserved(t *testing.T) {
	dir := t.TempDir()
	pluginName := "wirectl-probe"
	if runtime.GOOS == "windows" {
		pluginName += ".exe"
	}
	plugin := filepath.Join(dir, pluginName)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plugin, data, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRECTL_CLI_EXIT_HELPER", "23")
	t.Setenv("PATH", dir)

	app := App{Name: "wirectl", PluginPrefix: "wirectl-"}
	err = app.Run(context.Background(), []string{"probe"})
	if err == nil {
		t.Fatal("plugin unexpectedly succeeded")
	}
	if got := ExitCode(err); got != 23 {
		t.Fatalf("ExitCode = %d, want 23 (error: %v)", got, err)
	}
}

func writePluginFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("plugin fixture\n"), 0755); err != nil {
		t.Fatal(err)
	}
}
