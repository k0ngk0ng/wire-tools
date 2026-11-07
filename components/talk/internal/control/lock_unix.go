//go:build !windows

package control

import (
	"fmt"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/private"
	"os"
	"path/filepath"
	"syscall"
)

func Lock(dir string) (func(), error) {
	if err := private.Dir(dir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "session.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("a talk session already owns this profile: %w", err)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
