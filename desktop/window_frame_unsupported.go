//go:build !linux || !cgo || !gtk3

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// ownWindowFrame is a no-op outside the Linux GTK3 build: Windows trims the
// native frame itself for a frameless window, and compositors on the other
// platforms have no KWin-style server-side decoration protocol to answer.
func ownWindowFrame(*application.WebviewWindow) {}
