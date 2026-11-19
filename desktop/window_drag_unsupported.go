//go:build !linux || !cgo || !gtk3

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// installTitlebarDrag is only needed on Linux GTK: on Windows Wails' own
// --wails-draggable path already moves the window from the page's title bar,
// and there is no GTK widget to hook.
func installTitlebarDrag(*application.WebviewWindow) {}
