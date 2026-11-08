package update

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// Failure scenarios: a package manager's cached catalog is stale; refresh
// fails; refresh succeeds but app upgrade fails; neither path may upgrade
// other applications or change our manual installation transaction.
func TestPackageManagerRefreshesBeforeOnlyWirectlUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture; Windows PowerShell sequence covered by CLI E2E")
	}
	for _, manager := range []string{"homebrew", "scoop"} {
		for _, failure := range []string{"", "refresh", "upgrade"} {
			t.Run(manager+"/"+failure, func(t *testing.T) {
				root := t.TempDir()
				binary := filepath.Join(root, "wirectl")
				if err := os.WriteFile(binary, []byte("original"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".wirectl-package-manager"), []byte(manager), 0600); err != nil {
					t.Fatal(err)
				}
				tools := filepath.Join(root, "tools")
				if err := os.Mkdir(tools, 0700); err != nil {
					t.Fatal(err)
				}
				name := "brew"
				expected := []string{"update", "upgrade k0ngk0ng/wire-tools/wirectl"}
				if manager == "scoop" {
					name = "scoop"
					expected = []string{"update", "update wirectl"}
				}
				program := `#!/bin/sh
printf '%s\n' "$*" >> "$WIRECTL_MANAGER_CALLS"
if [ "$#" -eq 1 ] && [ "$1" = update ]; then
  if [ "$WIRECTL_MANAGER_FAILURE" = refresh ]; then exit 23; fi
else
  if [ "$WIRECTL_MANAGER_FAILURE" = upgrade ]; then exit 24; fi
fi
`
				if err := os.WriteFile(filepath.Join(tools, name), []byte(program), 0755); err != nil {
					t.Fatal(err)
				}
				calls := filepath.Join(root, "calls")
				t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
				t.Setenv("WIRECTL_MANAGER_CALLS", calls)
				t.Setenv("WIRECTL_MANAGER_FAILURE", failure)
				result, err := Run(context.Background(), Options{Executable: binary})
				if failure == "" && err != nil {
					t.Fatal(err)
				}
				if failure != "" && err == nil {
					t.Fatal("package manager failure was ignored")
				}
				if failure == "refresh" {
					expected = expected[:1]
				}
				data, readErr := os.ReadFile(calls)
				if readErr != nil {
					t.Fatal(readErr)
				}
				got := strings.Split(strings.TrimSpace(string(data)), "\n")
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("calls %q, want %q", got, expected)
				}
				if failure == "" && result.PackageManager != manager {
					t.Fatal(result)
				}
				data, readErr = os.ReadFile(binary)
				if readErr != nil || string(data) != "original" {
					t.Fatalf("manual transaction changed: %q %v", data, readErr)
				}
			})
		}
	}
}
