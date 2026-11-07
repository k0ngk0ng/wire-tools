// Package private protects download credentials using native filesystem policy.
// Windows uses restricted DACLs; POSIX systems use owner-only permissions.
package private

import (
	"errors"
	"fmt"
	"os"
)

func Dir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private state must be a real directory: %s", path)
	}
	return protect(path, true)
}
func File(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("private state must be a regular file")
	}
	return protect(path, false)
}
func Check(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return errors.New("private state must be a regular file")
	}
	return checkPrivate(path, st)
}
func CheckDir(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("private state must be a real directory")
	}
	return checkPrivateDir(path, st)
}
