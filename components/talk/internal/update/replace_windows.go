package update

import (
	"fmt"
	"os"
	"path/filepath"
)

// Windows permits renaming a running image, but not replacing or deleting it.
// Preserve the old image until the last process using it exits.
func replace(staged, target string) error {
	backup, err := os.CreateTemp(filepath.Dir(target), ".talk-old-*.exe")
	if err != nil {
		return err
	}
	old := backup.Name()
	backup.Close()
	if err = os.Remove(old); err != nil {
		return err
	}
	if err = os.Rename(target, old); err != nil {
		return err
	}
	if err = os.Rename(staged, target); err != nil {
		if rollback := os.Rename(old, target); rollback != nil {
			return fmt.Errorf("update failed: %v; rollback failed: %v; previous executable: %s", err, rollback, old)
		}
		return err
	}
	os.Remove(old) // May remain until the next manual cleanup, if still in use.
	return nil
}
