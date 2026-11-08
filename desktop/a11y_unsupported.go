//go:build !linux

package main

// guardAccessibilityBridge is a no-op on systems whose desktop build does not
// use the Linux GTK3/AT-SPI stack; there is no accessibility bridge to guard.
func guardAccessibilityBridge() {}
