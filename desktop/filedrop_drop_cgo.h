#ifndef FLOWDROP_FILEDROP_DROP_CGO_H
#define FLOWDROP_FILEDROP_DROP_CGO_H

// Shared drag-action and drop-authorization rules for the inbox FileList
// bridge.
//
// Included by desktop/filedrop_linux_gtk3.go (production) and by the
// flowdrop_filedrop_probe wrapper used by the no-GUI regression test, so the
// test exercises exactly this code. GLib only: no GTK, no display, no window
// and no main loop.

#include <glib.h>

// flowdrop_drop_should_claim reports whether the native bridge takes over the
// action negotiation for this drag as well.
//
// Evidence (GTK 3.24 gtk/gtkwidget.c + gtk/gtkdnd.c, GDK 3.24
// gdk/wayland/gdkdnd-wayland.c, WebKitGTK DropTargetGtk3.cpp):
//
//   - The "drag-motion" signal uses the TRUE-handled accumulator, and the
//     WebKit WebView's own handler is connected with g_signal_connect_after.
//     A handler that returns TRUE takes responsibility for gdk_drag_status()
//     and stops WebKit's after-handler from running.
//   - WebKit's after-handler forwards every motion to the page and then calls
//     gdk_drag_status(context, dragOperationToSingleGdkDragAction(
//     page->currentDragOperation()), ...). A page that cannot build a File
//     from the drop reports DragOperation::None, i.e. action 0.
//   - On Wayland action 0 is remembered in selected_action and committed as
//     wl_data_offer.accept(serial, NULL) plus drop status "not accepted"
//     (gdk_wayland_drag_context_commit_status /
//     gdk_wayland_drop_context_set_status). The source/compositor then has no
//     accepted target and never delivers the drop at all, so "drag-drop" and
//     "drag-data-received" never fire.
//
// So the bridge must advertise COPY itself when it is the one consuming the
// drop. It must not advertise an action the source does not offer, so COPY is
// only claimed when the drag actually lists GDK_ACTION_COPY.
static gboolean flowdrop_drop_should_claim(gboolean originMatches, gboolean isFileDrag, gboolean sourceOffersCopy) {
	return originMatches && isFileDrag && sourceOffersCopy;
}

// Authorization of exactly one accepted drop. context is only ever compared by
// identity, never dereferenced; no path, URI or credential is stored here.
typedef struct {
	gpointer context;
	gint64 acceptedAt;
} flowdrop_drop_auth;

enum {
	// Not the drop this code accepted: WebKit's own hover read, or a drag that
	// was left to WebKit. The data stays untouched for WebKit's handler.
	FLOWDROP_DROP_DATA_IGNORE = 0,
	// A drop this code accepted, but its data window closed: drop it.
	FLOWDROP_DROP_DATA_EXPIRED = 1,
	// This drop's data: consume it now (the authorization is spent).
	FLOWDROP_DROP_DATA_CONSUME = 2,
};

static void flowdrop_drop_auth_clear(flowdrop_drop_auth *auth) {
	auth->context = NULL;
	auth->acceptedAt = 0;
}

// A new "drag-drop" revokes whatever an earlier drop left behind.
static void flowdrop_drop_auth_accept(flowdrop_drop_auth *auth, gpointer context, gint64 now) {
	auth->context = context;
	auth->acceptedAt = now;
}

// "drag-leave" must not revoke an accepted drop.
//
// GTK3 already emits leave before drag-drop for the same drop (gtkdnd.c,
// GDK_DROP_START: "We send a leave here so that the widget unhighlights"), and
// on Wayland leave is a separate compositor event
// (gdkdevice-wayland.c data_device_leave) that can arrive while the dropped
// file data is still travelling back from the source application through
// wl_data_offer.receive, i.e. BEFORE "drag-data-received". The data, not the
// leave, decides. Authorization is revoked by consumption, by the data-window
// timeout, by the next drop, by a page load and by view destroy.
static void flowdrop_drop_auth_leave(flowdrop_drop_auth *auth) {
	(void)auth;
}

static int flowdrop_drop_auth_data(flowdrop_drop_auth *auth, gpointer context, gint64 now, gint64 timeoutUs) {
	if (auth->context == NULL || auth->context != context) {
		return FLOWDROP_DROP_DATA_IGNORE;
	}
	if (auth->acceptedAt == 0 || now - auth->acceptedAt > timeoutUs) {
		flowdrop_drop_auth_clear(auth);
		return FLOWDROP_DROP_DATA_EXPIRED;
	}
	// One accepted drop authorizes exactly one data delivery.
	flowdrop_drop_auth_clear(auth);
	return FLOWDROP_DROP_DATA_CONSUME;
}

#endif
