//go:build !windows

package state

import "os"

func protect(path string) error { return os.Chmod(path, 0700) }
