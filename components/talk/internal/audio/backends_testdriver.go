//go:build talk_test_audio

package audio

import (
	"fmt"
	"github.com/gen2brain/malgo"
	"os"
)

// Integration-test-only driver. Release builds must never use this build tag.
// See audio_test.go for the upstream enum offset in pinned malgo v0.11.24.
func nativeBackends() []malgo.Backend { return []malgo.Backend{malgo.Backend(14)} }

// Simulate a native driver stuck during shutdown only in integration builds.
func beforeDeviceClose() {
	if os.Getenv("WIRE_TALK_TEST_HANG_CLOSE") == "1" {
		select {}
	}
}

// Integration tests simulate hot unplug without accessing physical devices.
func deviceFault(kind malgo.DeviceType) error {
	name := "WIRE_TALK_TEST_INPUT_OFFLINE"
	if kind == malgo.Playback {
		name = "WIRE_TALK_TEST_OUTPUT_OFFLINE"
	}
	if path := os.Getenv(name); path != "" {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("test device unplugged")
		}
	}
	return nil
}
