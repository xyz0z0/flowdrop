#ifndef FLOWDROP_FILEDROP_SIGNALS_CGO_H
#define FLOWDROP_FILEDROP_SIGNALS_CGO_H

// Shared GObject signal helpers for the inbox file-drop restore.
//
// Included by desktop/filedrop_linux_gtk3.go (production) and by the
// flowdrop_filedrop_probe wrapper used by the no-GUI regression test, so the
// test exercises exactly this code. Only GObject/GLib is needed here: no GTK
// display, no window and no main loop.

#include <glib-object.h>

// flowdrop_match_count counts the handlers matching (signal id, user_data)
// exactly, without changing the surviving state: the matched handlers are
// blocked and immediately unblocked again, and nothing can run in between
// because this only ever runs on the GTK main thread.
//
// Signature note: g_signal_handlers_{block,unblock,disconnect}_matched is
//   (instance, mask, signal_id, detail, closure, func, data)
// so the user_data value Wails used must be the LAST argument; putting it in
// the func slot matches nothing (func would be compared, data would be NULL).
static int flowdrop_match_count(GObject *object, guint signalId, gpointer data) {
	int blocked = g_signal_handlers_block_matched(object,
		G_SIGNAL_MATCH_ID | G_SIGNAL_MATCH_DATA, signalId, 0, NULL, NULL, data);
	if (blocked > 0) {
		g_signal_handlers_unblock_matched(object,
			G_SIGNAL_MATCH_ID | G_SIGNAL_MATCH_DATA, signalId, 0, NULL, NULL, data);
	}
	return blocked;
}

static int flowdrop_disconnect_matched(GObject *object, guint signalId, gpointer data) {
	return g_signal_handlers_disconnect_matched(object,
		G_SIGNAL_MATCH_ID | G_SIGNAL_MATCH_DATA, signalId, 0, NULL, NULL, data);
}

#endif
