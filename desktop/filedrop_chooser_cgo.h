#ifndef FLOWDROP_FILEDROP_CHOOSER_CGO_H
#define FLOWDROP_FILEDROP_CHOOSER_CGO_H

// Shared "who owns this file chooser request" decision for the native FileList
// bridge.
//
// Included by desktop/filedrop_linux_gtk3.go (production) and by the
// flowdrop_filedrop_probe wrapper used by the no-GUI regression test, so the
// test exercises exactly this code. GLib strings only: no GTK, no display.

#include <glib.h>

// Prefix of the per-drop accept MIME token the bridge puts on its hidden file
// input (see flowdrop_drop_token_new in filedrop_linux_gtk3.go).
#define FLOWDROP_DROP_TOKEN_PREFIX "application/x-flowdrop-drop-"

enum {
	// Not a request the bridge knows about: an ordinary page file input.
	// WebKit's default handler must open the normal file dialog.
	FLOWDROP_CHOOSER_DEFAULT = 0,
	// Request carries exactly the current drop token: answer it natively with
	// the pending drop paths instead of a dialog.
	FLOWDROP_CHOOSER_SELECT_DROP = 1,
	// Request carries a bridge token from an earlier/expired drop: cancel it
	// so a stale modal file dialog never appears.
	FLOWDROP_CHOOSER_CANCEL_STALE = 2,
};

// flowdrop_chooser_decision classifies a run-file-chooser request by its MIME
// list. The current token wins over the stale check; a token from any other
// drop batch must never be answered with this batch's files, and an ordinary
// request (no bridge token at all) must keep the default dialog.
static int flowdrop_chooser_decision(const gchar *const *mimeTypes, const gchar *currentToken) {
	int stale = 0;
	if (mimeTypes == NULL) {
		return FLOWDROP_CHOOSER_DEFAULT;
	}
	for (int i = 0; mimeTypes[i] != NULL; i++) {
		const gchar *mime = mimeTypes[i];
		if (mime == NULL) {
			continue;
		}
		if (currentToken != NULL && g_strcmp0(mime, currentToken) == 0) {
			return FLOWDROP_CHOOSER_SELECT_DROP;
		}
		if (g_str_has_prefix(mime, FLOWDROP_DROP_TOKEN_PREFIX)) {
			stale = 1;
		}
	}
	return stale ? FLOWDROP_CHOOSER_CANCEL_STALE : FLOWDROP_CHOOSER_DEFAULT;
}

#endif
