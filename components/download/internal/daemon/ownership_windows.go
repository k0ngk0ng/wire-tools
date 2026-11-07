//go:build windows

package daemon

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Only the dedicated watchdog calls this function. The raw job handle remains
// open until process exit: explicitly closing its last handle would also kill
// the watchdog itself. Children inherit this nested job before a package-manager
// shim has any opportunity to create the real native process.
var engineProcessJob windows.Handle

func protectEngineProcessTree() error {
	if engineProcessJob != 0 {
		return nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return fmt.Errorf("create native engine job: %w", err)
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("protect native engine job: %w", err)
	}
	if err = windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		windows.CloseHandle(job)
		return fmt.Errorf("assign native engine watchdog to job: %w", err)
	}
	engineProcessJob = job
	return nil
}
