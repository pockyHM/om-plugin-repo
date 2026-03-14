//go:build !linux

package main

// watchDmesg is a no-op on non-Linux platforms.
func watchDmesg() {
	emitEvent("dmesg monitoring not supported on this platform", map[string]string{
		"platform": "non-linux",
	})
}
