package daemon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/k0ngk0ng/wire-tools/components/download/internal/engine"
	"github.com/k0ngk0ng/wire-tools/components/download/internal/private"
)

const aria2ResumeFile = "resume-after-shutdown.json"

type aria2ResumeIntent struct {
	Version int      `json:"version"`
	GIDs    []string `json:"gids"`
}
type aria2TaskState struct {
	GID    string `json:"gid"`
	Status string `json:"status"`
}

func readAria2ResumeIntent(dir string) (aria2ResumeIntent, error) {
	intent := aria2ResumeIntent{Version: 1, GIDs: []string{}}
	path := filepath.Join(dir, aria2ResumeFile)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return intent, nil
	} else if err != nil {
		return intent, err
	}
	if err := private.Check(path); err != nil {
		return intent, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return intent, err
	}
	if stat.Size() > 1<<20 {
		return intent, errors.New("native resume intent is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return intent, err
	}
	if err = json.Unmarshal(data, &intent); err != nil {
		return intent, err
	}
	if intent.Version != 1 {
		return intent, errors.New("unsupported native resume intent version")
	}
	for _, gid := range intent.GIDs {
		if len(gid) != 16 {
			return intent, errors.New("invalid native resume GID")
		}
		if _, err = hex.DecodeString(gid); err != nil {
			return intent, errors.New("invalid native resume GID")
		}
	}
	return intent, nil
}
func freezeAria2BeforeShutdown(ctx context.Context, aria *engine.Aria2, dir string) error {
	intent, err := readAria2ResumeIntent(dir)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, gid := range intent.GIDs {
		ids[gid] = true
	}
	var active []aria2TaskState
	if err = aria.Call(ctx, "tellActive", nil, &active); err != nil {
		return err
	}
	for _, task := range active {
		if task.Status == "active" {
			ids[task.GID] = true
		}
	}
	for offset := 0; ; offset += 1000 {
		var waiting []aria2TaskState
		if err = aria.Call(ctx, "tellWaiting", []any{offset, 1000}, &waiting); err != nil {
			return err
		}
		for _, task := range waiting {
			if task.Status == "waiting" {
				ids[task.GID] = true
			}
		}
		if len(waiting) < 1000 {
			break
		}
	}
	intent.GIDs = intent.GIDs[:0]
	for gid := range ids {
		intent.GIDs = append(intent.GIDs, gid)
	}
	sort.Strings(intent.GIDs)
	data, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	// Persist intent before introducing automatic pauses. A watchdog/API crash
	// at any subsequent instruction can still distinguish them from user pauses.
	if err = atomicWrite(filepath.Join(dir, aria2ResumeFile), data); err != nil {
		return err
	}
	if err = aria.Call(ctx, "forcePauseAll", nil, nil); err != nil {
		return err
	}
	for {
		active = nil
		if err = aria.Call(ctx, "tellActive", nil, &active); err != nil {
			return err
		}
		if len(active) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// resumeAria2AfterShutdown restores only work which was active or waiting when
// the watchdog froze the engine. Tasks already paused by the user stay paused.
func resumeAria2AfterShutdown(ctx context.Context, aria *engine.Aria2, dir string) error {
	path := filepath.Join(dir, aria2ResumeFile)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	intent, err := readAria2ResumeIntent(dir)
	if err != nil {
		return err
	}
	restore, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, gid := range intent.GIDs {
		for {
			var task aria2TaskState
			err = aria.Call(restore, "tellStatus", []any{gid}, &task)
			if err == nil {
				if task.Status == "paused" {
					err = aria.Resume(restore, gid)
				}
				if err == nil {
					break
				}
			}
			select {
			case <-restore.Done():
				return fmt.Errorf("restore native task %s: %w", gid, restore.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	// Persist the now-active flags before retiring intent, making retries safe
	// across a second crash during restart as well as during shutdown.
	if err = aria.SaveSession(restore); err != nil {
		return err
	}
	return os.Remove(path)
}
