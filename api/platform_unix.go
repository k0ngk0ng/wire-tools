//go:build !windows

package api

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func detach(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func checkEndpointMode(st os.FileInfo) error {
	if st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("API endpoint must be private (mode 0600)")
	}
	return nil
}
