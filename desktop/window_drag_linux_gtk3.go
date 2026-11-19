//go:build linux && cgo && gtk3

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

// The page draws its own 36px title bar instead of a WM decoration, so GTK has
// no title-bar widget to move the window from: the only thing that can move it
// is "gtk_window_begin_move_drag" on a real button press. Wails' runtime asks
// for exactly that from JavaScript (the --wails-draggable CSS property), but
// the request carries the button/root-position/time it recorded in a GTK
// "button-press-event" handler — and on these windows that record never
// arrives, so the compositor gets a press with no valid button and timestamp
// (button 0) and silently ignores it. Pressing and dragging the bar therefore
// did nothing, while its buttons (plain DOM clicks) kept working.
//
// This handler starts the move from the press itself, on the same widget Wails
// listens on, so the values are this press's: the window moves on X11 and on
// Wayland alike, where the compositor needs the serial of a real press.
#define FLOWDROP_DRAG_BAR_HEIGHT 36
// The three window buttons sit in the bar's right end (3 x 34px + gaps +
// padding). A press there must stay a click, not the start of a move.
#define FLOWDROP_DRAG_BUTTONS_WIDTH 132
#define FLOWDROP_DRAG_MARKER "flowdrop-titlebar-drag"

static gboolean flowdrop_titlebar_press(GtkWidget *widget, GdkEventButton *event, gpointer data) {
	(void)data;
	if (event->type != GDK_BUTTON_PRESS || event->button != 1) {
		return FALSE;
	}
	// Event coordinates are in the widget's own logical pixels, the same unit
	// the title bar's CSS height is written in.
	if (!(event->y >= 0 && event->y < FLOWDROP_DRAG_BAR_HEIGHT)) {
		return FALSE;
	}
	GtkAllocation allocation;
	gtk_widget_get_allocation(widget, &allocation);
	if (event->x < 0 || event->x >= allocation.width - FLOWDROP_DRAG_BUTTONS_WIDTH) {
		return FALSE;
	}
	GtkWidget *toplevel = gtk_widget_get_toplevel(widget);
	if (toplevel == NULL || !GTK_IS_WINDOW(toplevel)) {
		return FALSE;
	}
	gtk_window_begin_move_drag(GTK_WINDOW(toplevel), (gint)event->button,
		(gint)event->x_root, (gint)event->y_root, (guint32)event->time);
	// FALSE: the page still receives its press (the bar has no press handler of
	// its own, and the move gesture now belongs to the window manager).
	return FALSE;
}

// flowdrop_titlebar_find_view returns the first WebKitWebView below root,
// following the GtkApplicationWindow -> GtkBox -> WebView tree Wails builds.
// It mirrors flowdrop_find_webview in filedrop_linux_gtk3.go; cgo gives every
// file its own translation unit, so the helper cannot be shared directly.
static GtkWidget *flowdrop_titlebar_find_view(GtkWidget *root, int depth) {
	if (root == NULL || depth > 8) {
		return NULL;
	}
	if (WEBKIT_IS_WEB_VIEW(root)) {
		return root;
	}
	if (!GTK_IS_CONTAINER(root)) {
		return NULL;
	}
	GtkWidget *found = NULL;
	GList *children = gtk_container_get_children(GTK_CONTAINER(root));
	for (GList *item = children; item != NULL && found == NULL; item = item->next) {
		found = flowdrop_titlebar_find_view(GTK_WIDGET(item->data), depth + 1);
	}
	g_list_free(children);
	return found;
}

// Return: 0 installed now, 1 already installed on this view, -1 no window,
// -2 no WebKitWebView found.
static int flowdrop_install_titlebar_drag(GtkWidget *window) {
	if (window == NULL) {
		return -1;
	}
	GtkWidget *view = flowdrop_titlebar_find_view(window, 0);
	if (view == NULL) {
		return -2;
	}
	if (g_object_get_data(G_OBJECT(view), FLOWDROP_DRAG_MARKER) != NULL) {
		return 1;
	}
	g_signal_connect_data(view, "button-press-event", G_CALLBACK(flowdrop_titlebar_press),
		NULL, NULL, 0);
	g_object_set_data(G_OBJECT(view), FLOWDROP_DRAG_MARKER, GINT_TO_POINTER(1));
	return 0;
}
*/
import "C"

import (
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const (
	titlebarDragInstalled = 0
	titlebarDragNoWork    = 1
	titlebarDragNoWindow  = -1
	titlebarDragNoView    = -2
)

// installTitlebarDrag makes the page's own title bar move the window. It waits
// for the window's first finished page load — the WebView widget only exists by
// then, and the hook does not depend on the page's JavaScript (the inbox window
// serves the Hub's HTTP origin, not Wails' asset server) — then hops onto the
// GTK main thread. Later loads fire it again; the install is idempotent per
// view.
func installTitlebarDrag(w *application.WebviewWindow) {
	if w == nil {
		return
	}
	w.RegisterHook(events.Linux.WindowLoadFinished, func(*application.WindowEvent) {
		application.InvokeAsync(func() { installTitlebarDragNow(w) })
	})
}

// installTitlebarDragNow must run on the GTK main thread.
func installTitlebarDragNow(w *application.WebviewWindow) {
	native := w.NativeWindow()
	if native == nil {
		log.Printf("window drag: native GTK window not available; the title bar will not move the window")
		return
	}
	switch code := int(C.flowdrop_install_titlebar_drag((*C.GtkWidget)(native))); code {
	case titlebarDragInstalled:
		log.Printf("window drag: title bar starts a window move on press")
	case titlebarDragNoWork:
		// Every later page load lands here; nothing to report.
	case titlebarDragNoView:
		log.Printf("window drag: no WebKitWebView found in the window; the title bar will not move the window")
	default:
		log.Printf("window drag: no GTK window to install on (%d); the title bar will not move the window", code)
	}
}
