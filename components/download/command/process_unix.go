//go:build !windows

package command

import (
	"os/exec"
	"syscall"
)

func privateUmask()               { syscall.Umask(0077) }
func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
