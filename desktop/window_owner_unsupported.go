//go:build !linux || !cgo || !gtk3

package main

import (
	"log"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	// settingsDialogOK is never returned on these builds: there is no GTK window
	// to attach to.
	settingsDialogOK = 0
	// settingsDialogNoWindow means the window could not be made a transient
	// dialog; the settings window keeps behaving as a plain top-level window.
	settingsDialogNoWindow = 1
)

// One warning per problem, not one per click.
var (
	settingsAttachWarning  sync.Once
	settingsPresentWarning sync.Once
)

// attachSettingsDialog is unavailable outside the Linux GTK3 build: Windows and
// macOS own the parent/child window relationship through their own native APIs,
// and the pure-Go Linux build has no GTK window handle to hand to
// gtk_window_set_transient_for. The settings window therefore stays a separate
// top-level window there instead of failing.
func attachSettingsDialog(inbox, settings *application.WebviewWindow) int {
	if inbox == nil || settings == nil {
		return settingsDialogNoWindow
	}
	settingsAttachWarning.Do(func() {
		log.Printf("settings dialog: transient dialogs are only implemented on the Linux GTK3 build; the settings window stays a separate top-level window")
	})
	return settingsDialogNoWindow
}

// presentSettingsDialog is unavailable outside the Linux GTK3 build for the same
// reason; the caller keeps the plain Show/Focus path it had before.
func presentSettingsDialog(inbox, settings *application.WebviewWindow) int {
	if inbox == nil || settings == nil {
		return settingsDialogNoWindow
	}
	settingsPresentWarning.Do(func() {
		log.Printf("settings dialog: raising the dialog is only implemented on the Linux GTK3 build; the settings window keeps the plain show/focus path")
	})
	return settingsDialogNoWindow
}
