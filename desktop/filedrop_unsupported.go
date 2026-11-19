//go:build !linux || !cgo || !gtk3

package main

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// relaxInboxFileDrop is the stub for builds without the Linux GTK3/WebKitGTK
// backend. There is no GTK drop receiver and no GTK chooser callback to hook,
// so external file drops stay with whatever the backend does by default and the
// desktop logs that the native FileList bridge was not enabled.
func relaxInboxFileDrop(*application.WebviewWindow, string) {
	log.Printf("inbox file drop: native FileList bridge requires a linux + cgo + gtk3 build; nothing changed")
}
