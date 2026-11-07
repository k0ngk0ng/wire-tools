//go:build !windows

package daemon

func protectEngineProcessTree() error { return nil }
