//go:build windows

package main

// trayWatcherPresent reports whether the shell can show a system tray icon.
//
// Windows implements the notification area natively through Shell_NotifyIcon
// (wrapped by the Wails Windows systray backend); there is no
// StatusNotifierWatcher/DBus indirection and therefore no runtime component
// that could be missing. The tray is always available here, so the shell keeps
// the "close hides to tray" behaviour instead of the confirm-quit fallback.
func trayWatcherPresent() bool { return true }
