package state

import (
	"fmt"

	"path/filepath"
	"syscall"
)

func Lock(dir string) (func(), error) {
	if err := Dir(dir); err != nil {
		return nil, err
	}
	p, err := syscall.UTF16PtrFromString(filepath.Join(dir, "runtime.lock"))
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("runtime lock is held: %w", err)
	}
	return func() { syscall.CloseHandle(h) }, nil
}
