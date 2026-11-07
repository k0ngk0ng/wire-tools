package control

import (
	"fmt"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/private"
	"path/filepath"
	"syscall"
)

func Lock(dir string) (func(), error) {
	if err := private.Dir(dir); err != nil {
		return nil, err
	}
	p, err := syscall.UTF16PtrFromString(filepath.Join(dir, "session.lock"))
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, fmt.Errorf("a talk session already owns this profile: %w", err)
	}
	return func() { syscall.CloseHandle(h) }, nil
}
