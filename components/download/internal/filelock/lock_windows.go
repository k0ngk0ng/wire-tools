//go:build windows

package filelock

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func Try(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
}
func Busy(err error) bool { return errors.Is(err, windows.ERROR_LOCK_VIOLATION) }
