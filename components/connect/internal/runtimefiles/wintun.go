// Package runtimefiles selects connect dependencies from the executable's
// immutable distribution, without consulting PATH or the working directory.
package runtimefiles

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Wintun returns the DLL and license belonging to an entry executable. Only
// the unified wirectl entry can select an immutable generation; protected
// helper copies always use their adjacent runtime, even with inherited env.
// The caller validates file types and loads/copies these exact paths. A
// missing selected file must never fall back to an older adjacent runtime.
func Wintun(executable, bundle string) (dll, license string, err error) {
	if !filepath.IsAbs(executable) {
		return "", "", errors.New("Wintun executable path must be absolute")
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", "", fmt.Errorf("resolve Wintun executable: %w", err)
	}
	directory := filepath.Dir(executable)
	name := filepath.Base(executable)
	unified := strings.EqualFold(name, "wirectl.exe") || strings.EqualFold(name, "wirectl")
	if unified && bundle != "" {
		if !filepath.IsAbs(bundle) {
			return "", "", errors.New("Wintun generation path must be absolute")
		}
		bundle, err = filepath.EvalSymlinks(bundle)
		if err != nil {
			return "", "", fmt.Errorf("resolve Wintun generation: %w", err)
		}
		root := directory
		if strings.EqualFold(filepath.Base(root), "bin") {
			root = filepath.Dir(root)
		}
		versions := filepath.Join(root, ".wirectl", "versions")
		relative, relErr := filepath.Rel(versions, bundle)
		if relErr != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.ContainsAny(relative, `/\`) {
			return "", "", errors.New("Wintun generation must be a direct child of the executable installation's .wirectl/versions directory")
		}
		directory = filepath.Join(bundle, "bin")
	}
	return filepath.Join(directory, "wintun.dll"), filepath.Join(directory, "WINTUN-LICENSE.txt"), nil
}
