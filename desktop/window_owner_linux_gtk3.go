//go:build linux && cgo && gtk3

package main

/*
#cgo pkg-config: gtk+-3.0

#include <gtk/gtk.h>

// Why this file exists: the settings window is a second top-level window, so it
// can end up behind the inbox, shows up as a separate entry in Alt-Tab and lands
// wherever the window manager puts it. Making it a transient dialog of the inbox
// fixes all three: the window manager keeps a transient above its parent, keeps
// it off the task bar, and (with the CENTER_ON_PARENT constraint) places it over
// the parent. Nothing here restores or re-creates the windows: it only sets the
// parent link and the position constraint on the existing ones.

#define FLOWDROP_SETTINGS_PARENT_MARKER "flowdrop-settings-transient-parent"

// flowdrop_settings_attach marks settings as a transient dialog of inbox and asks
// for it to be centred over the inbox.
// The marker stores the parent the window was attached to (not just "done"), so
// repeated calls on the same pair do nothing while a re-created inbox window
// re-attaches instead of being skipped.
// Return: 0 attached now, 1 already attached to this parent, -1 not two windows.
static int flowdrop_settings_attach(GtkWidget *inbox, GtkWidget *settings) {
	if (inbox == NULL || settings == NULL ||
		!GTK_IS_WINDOW(inbox) || !GTK_IS_WINDOW(settings)) {
		return -1;
	}
	if (g_object_get_data(G_OBJECT(settings), FLOWDROP_SETTINGS_PARENT_MARKER) == (gpointer)inbox) {
		return 1;
	}
	// The transient parent is what turns settings into a dialog of the inbox: the
	// window manager then keeps it above the inbox, can keep it out of the task
	// switcher, and hides it together with the parent.
	gtk_window_set_transient_for(GTK_WINDOW(settings), GTK_WINDOW(inbox));
	// Position comes after the parent on purpose: GTK_WIN_POS_CENTER_ON_PARENT
	// without a transient parent falls back to centring on the screen, so setting
	// it first would centre the dialog on the wrong thing. The constraint is
	// turned into a position hint when the window is mapped, so it must be in
	// place before the first show. Letting the compositor place the dialog over
	// its parent is also the only placement that works on Wayland, where a
	// frameless client may not move itself.
	gtk_window_set_position(GTK_WINDOW(settings), GTK_WIN_POS_CENTER_ON_PARENT);
	// A window that is already on screen is not moved by a new position
	// constraint: GTK only repositions for CENTER_ALWAYS. Compute the centre of
	// the inbox and move the dialog there. X11 window managers honour
	// gtk_window_move; Wayland clients cannot move themselves at all, so there the
	// compositor's own transient placement (from the hint above) is what centres
	// the dialog.
	if (gtk_widget_get_mapped(GTK_WIDGET(settings))) {
		gint inboxX = 0, inboxY = 0, inboxWidth = 0, inboxHeight = 0;
		gint settingsWidth = 0, settingsHeight = 0;
		gtk_window_get_position(GTK_WINDOW(inbox), &inboxX, &inboxY);
		gtk_window_get_size(GTK_WINDOW(inbox), &inboxWidth, &inboxHeight);
		gtk_window_get_size(GTK_WINDOW(settings), &settingsWidth, &settingsHeight);
		if (inboxWidth > 0 && inboxHeight > 0) {
			gtk_window_move(GTK_WINDOW(settings),
				inboxX + (inboxWidth - settingsWidth) / 2,
				inboxY + (inboxHeight - settingsHeight) / 2);
		}
	}
	g_object_set_data(G_OBJECT(settings), FLOWDROP_SETTINGS_PARENT_MARKER, inbox);
	return 0;
}

// flowdrop_settings_present raises the already existing settings dialog.
// Return: 0 presented with the current event time, 1 presented without one,
// -1 not a window.
static int flowdrop_settings_present(GtkWidget *inbox, GtkWidget *settings) {
	if (settings == NULL || !GTK_IS_WINDOW(settings)) {
		return -1;
	}
	// Bringing the dialog up must not detach it from the inbox, and a raise is a
	// common first contact too: attach here when that has not happened yet (or
	// when the inbox window was re-created). The call is idempotent.
	flowdrop_settings_attach(inbox, settings);
	guint32 eventTime = gtk_get_current_event_time();
	if (eventTime == 0) {
		// No user event is in flight (a tray menu item, a timer, an accelerator
		// handled outside GTK). A 0 timestamp is a request the compositor may drop
		// as stale, so ask for a plain present and let the window manager choose
		// its own serial.
		gtk_window_present(GTK_WINDOW(settings));
		return 1;
	}
	// The event time is the focus-stealing-prevention token: it says this raise
	// answers a real click/press, so the window manager may put the dialog above
	// the inbox. Nothing here grabs focus, runs page JS or reads the page; the
	// compositor decides where the dialog goes and whether it takes focus.
	gtk_window_present_with_time(GTK_WINDOW(settings), eventTime);
	return 0;
}
*/
import "C"

import (
	"log"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	// settingsDialogOK is the return value of a queued request: both windows are
	// real, so the GTK call runs on the main thread.
	settingsDialogOK = 0
	// settingsDialogNoWindow means a Wails window handle (or its native GTK
	// window) was not available; the settings window keeps behaving as a plain
	// top-level window, which is what it was before this file existed.
	settingsDialogNoWindow = 1
)

// One warning per problem, not one per click: "设置" can be pressed repeatedly
// and a failed attach must not fill the log.
var (
	settingsAttachWarning  sync.Once
	settingsPresentWarning sync.Once
)

// attachSettingsDialog makes the settings window a transient dialog of the inbox
// and centres it over the inbox.
//
// It returns settingsDialogOK once the request is queued on the GTK main thread,
// and settingsDialogNoWindow when a Wails window handle is missing (the failure
// is logged once, never panics). The result of the GTK call itself cannot be
// returned because the work is asynchronous. A native handle that does not exist
// yet is waited for (native_window_retry.go) instead of being treated as
// failure, which is what the first post-startup call used to hit.
//
// The windows exist before app.Run, so InvokeSync here would queue its work in
// Wails' earlyDispatch list, which is drained from inside Run: waiting for it
// deadlocks the start (window_frame_linux_gtk3.go and docs/DEPLOY.md record the
// same trap). InvokeAsync runs the call on the GTK main thread once there is a
// main loop.
//
// Call it before the dialog is first shown: the CENTER_ON_PARENT constraint is
// turned into a position hint when the window is mapped.
func attachSettingsDialog(inbox, settings *application.WebviewWindow) int {
	if inbox == nil || settings == nil {
		settingsAttachWarning.Do(func() {
			log.Printf("settings dialog: no inbox/settings window handle; the settings window stays a separate top-level window")
		})
		return settingsDialogNoWindow
	}
	// Both native handles may still be missing right after window creation, so
	// wait for them on the GTK thread (native_window_retry.go).
	withNativeWindows("settings dialog", []*application.WebviewWindow{inbox, settings}, func(handles []unsafe.Pointer) {
		inboxWindow := (*C.GtkWidget)(handles[0])
		settingsWindow := (*C.GtkWidget)(handles[1])
		switch int(C.flowdrop_settings_attach(inboxWindow, settingsWindow)) {
		case 0:
			log.Printf("settings dialog: attached to the inbox and centred on it")
		case 1:
			// Already attached to this inbox (every later call lands here).
		default:
			settingsAttachWarning.Do(func() {
				log.Printf("settings dialog: GTK window handles are not a GtkWindow; the settings window stays a separate top-level window")
			})
		}
	})
	return settingsDialogOK
}

// presentSettingsDialog raises the existing settings window on the current
// desktop / in front of the inbox. It re-attaches the dialog first when that has
// not happened yet, and never panics: a missing handle returns
// settingsDialogNoWindow and logs once.
func presentSettingsDialog(inbox, settings *application.WebviewWindow) int {
	if inbox == nil || settings == nil {
		settingsPresentWarning.Do(func() {
			log.Printf("settings dialog: no inbox/settings window handle; cannot bring the settings window to the front")
		})
		return settingsDialogNoWindow
	}
	withNativeWindows("settings dialog", []*application.WebviewWindow{inbox, settings}, func(handles []unsafe.Pointer) {
		inboxWindow := (*C.GtkWidget)(handles[0])
		settingsWindow := (*C.GtkWidget)(handles[1])
		if int(C.flowdrop_settings_present(inboxWindow, settingsWindow)) < 0 {
			settingsPresentWarning.Do(func() {
				log.Printf("settings dialog: GTK window handles are not a GtkWindow; cannot bring the settings window to the front")
			})
		}
	})
	return settingsDialogOK
}
