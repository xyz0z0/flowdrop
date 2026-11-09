//go:build !linux && !windows

package main

// trayWatcherPresent reports whether the shell can show a system tray icon.
//
// On platforms without a tray probe implementation (macOS, BSD, ...) the shell
// cannot tell whether the Wails tray will really appear. It stays on the
// existing conservative semantics: report no tray, so closing the last window
// asks the user to confirm quitting instead of hiding an unreachable window.
func trayWatcherPresent() bool { return false }
