//go:build windows

package command

import (
	"os/exec"
	"syscall"
)

func privateUmask() {}
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
