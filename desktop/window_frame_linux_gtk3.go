//go:build linux && cgo && gtk3

package main

/*
#cgo pkg-config: gtk+-3.0

#include <gtk/gtk.h>

#ifdef GDK_WINDOWING_WAYLAND
#include <gdk/gdkwayland.h>
#endif

// Why this file exists: on a KDE Plasma Wayland session GTK 3 asks KWin's
// decoration protocol for a server-drawn frame on any window that has none of
// its own — an undecorated one included. KWin then drew its title bar (with
// minimise/maximise/close) on top of FlowDrop's frameless window, exactly the
// bar the page's own title bar was meant to replace. magpie hit the same KWin
// behaviour (#1283) and fixes it by announcing a client-side frame on realize.
//
// The call is a no-op on X11 and on compositors without that protocol (GNOME,
// Hyprland), so the frameless window keeps working there unchanged.

#define FLOWDROP_FRAME_MARKER "flowdrop-own-frame"

static void flowdrop_announce_csd(GtkWidget *widget, gpointer data) {
	(void)data;
#ifdef GDK_WINDOWING_WAYLAND
	GdkWindow *gdkWindow = gtk_widget_get_window(widget);
	if (gdkWindow != NULL && GDK_IS_WAYLAND_WINDOW(gdkWindow)) {
		// Visible when launched from a terminal: it is the one line that says
		// whether KWin was told to keep its decorations off this window.
		g_printerr("window frame: announced client-side frame to the Wayland compositor\n");
		gdk_wayland_window_announce_csd(gdkWindow);
	}
#endif
}

// flowdrop_own_frame marks the window as client-decorated now (if it is
// already realized) and after every later realize. GTK 3 re-announces a
// server-side frame on each realize, so a one-shot call would be undone.
static void flowdrop_own_frame(GtkWidget *widget) {
	if (g_object_get_data(G_OBJECT(widget), FLOWDROP_FRAME_MARKER) == NULL) {
		g_signal_connect_after(widget, "realize", G_CALLBACK(flowdrop_announce_csd), NULL);
		g_object_set_data(G_OBJECT(widget), FLOWDROP_FRAME_MARKER, GINT_TO_POINTER(1));
	}
	if (gtk_widget_get_realized(widget)) {
		flowdrop_announce_csd(widget, NULL);
	}
}
*/
import "C"

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ownWindowFrame keeps a frameless FlowDrop window frameless on KDE Plasma's
// Wayland session (KWin otherwise draws its own title bar on it).
//
// It is queued with InvokeAsync rather than InvokeSync: the windows are created
// before app.Run, and InvokeSync before the main loop only queues its work in
// Wails' earlyDispatch list, which is drained from inside Run — waiting there
// deadlocks the start (downloads_linux_gtk3.go documents the same trap). The
// async task runs after the window exists (native_window_retry.go waits for the
// native handle instead of dropping the announce on the first miss), and the C
// side keeps a marker so a second call never connects the realize handler
// twice.
func ownWindowFrame(w *application.WebviewWindow) {
	if w == nil {
		return
	}
	// The native handle does not exist until the GTK main loop builds the
	// window, so wait for it instead of dropping the announce on the first miss
	// (native_window_retry.go).
	withNativeWindows("window frame", []*application.WebviewWindow{w}, func(handles []unsafe.Pointer) {
		C.flowdrop_own_frame((*C.GtkWidget)(handles[0]))
	})
}
