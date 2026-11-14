//go:build !talk_test_audio

package plugins

func testHardwareSupported(string) bool    { return false }
func sdkTestEnvironment() ([]string, bool) { return nil, false }
