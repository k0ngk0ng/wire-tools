//go:build !windows

package update

import "os"

func replace(staged, target string) error { return os.Rename(staged, target) }
