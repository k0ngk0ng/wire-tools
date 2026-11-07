//go:build windows

package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func init() {
	role := os.Getenv("WIRECTL_JOB_TEST_ROLE")
	if role == "" {
		return
	}
	dir := os.Getenv("WIRECTL_JOB_TEST_DIR")
	if role == "owner" {
		if err := protectEngineProcessTree(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(31)
		}
	}
	if role == "owner" || role == "shim" {
		self, err := os.Executable()
		if err != nil {
			os.Exit(32)
		}
		next := "shim"
		if role == "shim" {
			next = "native"
		}
		child := exec.Command(self)
		child.Env = append(os.Environ(), "WIRECTL_JOB_TEST_ROLE="+next)
		if err = child.Start(); err != nil {
			os.Exit(33)
		}
		data, _ := json.Marshal(child.Process.Pid)
		if os.WriteFile(filepath.Join(dir, next+".json"), data, 0600) != nil {
			os.Exit(34)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestWindowsWatchdogJobKillsShimAndNativeDescendant(t *testing.T) {
	dir := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	owner := exec.Command(self)
	owner.Env = append(os.Environ(), "WIRECTL_JOB_TEST_ROLE=owner", "WIRECTL_JOB_TEST_DIR="+dir)
	if err = owner.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Process.Kill(); _ = owner.Wait() }()
	handles := []windows.Handle{}
	for _, name := range []string{"shim", "native"} {
		path := filepath.Join(dir, name+".json")
		var pid int
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
			if b, readErr := os.ReadFile(path); readErr == nil && json.Unmarshal(b, &pid) == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if pid < 1 {
			t.Fatalf("%s descendant did not start", name)
		}
		handle, openErr := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
		if openErr != nil {
			t.Fatal(openErr)
		}
		handles = append(handles, handle)
		defer windows.CloseHandle(handle)
		defer windows.TerminateProcess(handle, 1)
	}
	if err = owner.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	for i, handle := range handles {
		result, waitErr := windows.WaitForSingleObject(handle, 5000)
		if waitErr != nil || result != windows.WAIT_OBJECT_0 {
			t.Fatalf("descendant %s survived watchdog crash: result=%d error=%v", strconv.Itoa(i), result, waitErr)
		}
	}
}
