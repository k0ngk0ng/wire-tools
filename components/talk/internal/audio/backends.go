//go:build !talk_test_audio

package audio

import (
	"github.com/gen2brain/malgo"
	"runtime"
)

// Explicit backends prevent miniaudio from silently falling back to a null
// device and reporting a successful session on a machine without audio.
func nativeBackends() []malgo.Backend {
	switch runtime.GOOS {
	case "darwin":
		return []malgo.Backend{malgo.BackendCoreaudio}
	case "windows":
		return []malgo.Backend{malgo.BackendWasapi, malgo.BackendDsound, malgo.BackendWinmm}
	default:
		return []malgo.Backend{malgo.BackendPulseaudio, malgo.BackendAlsa, malgo.BackendJack}
	}
}

func beforeDeviceClose() {}

func deviceFault(kind malgo.DeviceType) error { return nil }
