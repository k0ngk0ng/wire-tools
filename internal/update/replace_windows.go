package update

import (
	"fmt"
	"os"
	"path/filepath"
)

func replace(staged, target string) error {
	backup, err := os.CreateTemp(filepath.Dir(target), ".wirectl-old-*.exe")
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
	_ = os.Remove(old)
	return nil
}
