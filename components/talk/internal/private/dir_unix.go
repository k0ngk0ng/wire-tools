//go:build !windows

package private

import "os"

func protect(path string) error { return os.Chmod(path, 0700) }
