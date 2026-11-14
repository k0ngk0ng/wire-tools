package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestZshCompletionLoading(t *testing.T) {
	shell, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "_wirectl-talk"), []byte(zshCompletionScript), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"autoload", "source"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(shell, "-f", "-c", `
fpath=("$1" $fpath)
autoload -Uz compinit
compinit -D -i
if [[ "$2" == source ]]; then
    source "$1/_wirectl-talk"
fi
# Capture the candidates handed to zsh without requiring an interactive ZLE.
_describe() { print -rl -- "${(@P)2}"; }
for invocation in first second; do
    words=(wirectl talk '')
    CURRENT=3
    "${_comps[wirectl]}"
    words=(wirectl-talk group '')
    CURRENT=3
    "${_comps[wirectl-talk]}"
done
`, "completion-test", dir, mode)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			for _, want := range []string{"devices:list audio devices", "create"} {
				if strings.Count(string(out), want+"\n") != 2 {
					t.Fatalf("expected %q on first and second completion: %s", want, out)
				}
			}
		})
	}
}

func TestCompletionScripts(t *testing.T) {
	for _, test := range []struct {
		name   string
		script string
		want   []string
	}{
		{name: "bash", script: bashCompletionScript, want: []string{"_wirectl_talk_completion", "complete -o", "leave", "group", "levels", "--state-dir", "--ring-output"}},
		{name: "zsh", script: zshCompletionScript, want: []string{"_wirectl_talk_completion", "compdef", "_arguments", "leave", "group", "levels", "--state-dir", "--ring-output"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if strings.Contains(test.script, "daemon)") || strings.Contains(test.script, "'daemon:") {
				t.Error("completion still exposes manual daemon control")
			}
			for _, want := range test.want {
				if !strings.Contains(test.script, want) {
					t.Errorf("completion script does not contain %q", want)
				}
			}
		})
	}
}

func TestCompletionCommandRejectsUnsupportedShell(t *testing.T) {
	if err := completionCommand([]string{"fish"}); err == nil {
		t.Fatal("completionCommand(fish) returned nil")
	}
}
