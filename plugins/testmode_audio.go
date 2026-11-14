//go:build talk_test_audio

package plugins

import "os"

func testHardwareSupported(name string) bool {
	if name == "sakshat" {
		return os.Getenv("WIRECTL_TEST_SAKSHAT_SDK") != ""
	}
	return os.Getenv("WIRE_TALK_TEST_HANDSET_EVENTS") != ""
}

func sdkTestEnvironment() ([]string, bool) {
	if root := os.Getenv("WIRECTL_TEST_SAKSHAT_SDK"); root != "" {
		return []string{"PYTHONPATH=" + root}, true
	}
	return nil, false
}
