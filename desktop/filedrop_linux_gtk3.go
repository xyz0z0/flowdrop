//go:build linux && cgo && gtk3

package main

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.1

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <jsc/jsc.h>
#include <string.h>

#include "filedrop_signals_cgo.h"
#include "filedrop_chooser_cgo.h"
#include "filedrop_drop_cgo.h"

// This file does two things for the inbox window only:
//
//  1. removes exactly the two GTK drag-and-drop blockers that Wails v3.1.0
//     installs on a WebView when WebviewWindowOptions.Linux.EnableFileDrop is
//     false (linux_cgo_gtk3.go: disableDND -> "drag-drop" + "drag-motion",
//     each connected with the window id as gpointer user_data), and
//  2. receives the real GTK drop and turns it into a genuine FileList: the
//     dropped local paths stay in this C state, a hidden <input type=file>
//     is clicked through the page's bridge script
//     (desktopFileDropGuardJS), and WebKit's own "run-file-chooser" callback
//     answers that input with the pending native paths.
//
// Nothing else may be touched: no handler is removed by name, no other window
// is enumerated, and a shape that does not match exactly is left as is.
//
// Security boundaries kept by construction:
//   - A path can only enter pending state from a "drag-data-received" that GTK
//     delivers for the drag context this code itself accepted in "drag-drop"
//     (flowdrop_drop_auth). WebKit also requests drag data during hover
//     ("drag-motion") to build DataTransfer; those reads never authorize a
//     drop and never write pending state.
//   - Only local file:// URIs are turned into paths (g_filename_from_uri).
//     Remote/other URIs are dropped.
//   - The paths never reach JavaScript. Only a random MIME token and
//     percent-encoded directory basenames (hints) are passed to the page.
//   - The page can only ask for "the files of the drop the native side
//     already accepted": a run-file-chooser request is answered natively only
//     when its MIME list contains the current per-drop token, exactly.
//   - Pending state is cleared on consumption, timeout, page load start, and
//     view destruction, so a stale drop can never be handed to a later
//     ordinary file chooser.
//   - The bridge also owns the action negotiation for its own file drags: a
//     drop that nobody advertises is never delivered on Wayland, and WebKit's
//     after-handler would replace the action with the page's (empty) drop
//     operation. See filedrop_drop_cgo.h for the source evidence.
//   - All of it runs on the GTK main thread; no Go pointer is stored in C and
//     no C pointer is stored in Go.

// flowdrop_find_webview returns the first WebKitWebView below root, following
// the GtkApplicationWindow -> GtkBox (webview-box) -> [GtkMenuBar, WebView]
// tree Wails builds. Returns NULL when there is none.
static GtkWidget *flowdrop_find_webview(GtkWidget *root, int depth) {
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
		found = flowdrop_find_webview(GTK_WIDGET(item->data), depth + 1);
	}
	g_list_free(children);
	return found;
}

// The (signal id, user_data) matching helpers live in
// filedrop_signals_cgo.h so the no-GUI regression test can call them too.

// flowdrop_relax_inbox_dnd restores WebKit's default HTML5 file drag and drop
// on the WebView that belongs to the given GtkWindow. The window id must be the
// same value Wails passed as user_data, so only Wails' own blockers match.
//
// Return: 0 relaxed now, 1 nothing to do (already relaxed), <0 unchanged.
static int flowdrop_relax_inbox_dnd(GtkWidget *window, uintptr_t windowId,
	int *dragDropRemoved, int *dragMotionRemoved) {
	static const gchar *marker = "flowdrop-inbox-file-drop-relaxed";

	*dragDropRemoved = 0;
	*dragMotionRemoved = 0;
	if (window == NULL) {
		return -1;
	}
	GtkWidget *view = flowdrop_find_webview(window, 0);
	if (view == NULL) {
		return -2;
	}
	if (g_object_get_data(G_OBJECT(view), marker) != NULL) {
		return 1;
	}
	guint dragDropId = g_signal_lookup("drag-drop", G_OBJECT_TYPE(view));
	guint dragMotionId = g_signal_lookup("drag-motion", G_OBJECT_TYPE(view));
	if (dragDropId == 0 || dragMotionId == 0) {
		return -3;
	}
	gpointer data = (gpointer)windowId;
	int dragDropCount = flowdrop_match_count(G_OBJECT(view), dragDropId, data);
	int dragMotionCount = flowdrop_match_count(G_OBJECT(view), dragMotionId, data);
	// EnableFileDrop=false installs exactly one handler per signal. Zero or
	// multiple matches on an unmarked view is not the shape this code
	// understands: leave everything untouched and report it (never claim the
	// default WebKit drop is active without evidence).
	if (dragDropCount != 1 || dragMotionCount != 1) {
		return -4;
	}
	*dragDropRemoved = flowdrop_disconnect_matched(G_OBJECT(view), dragDropId, data);
	*dragMotionRemoved = flowdrop_disconnect_matched(G_OBJECT(view), dragMotionId, data);
	if (*dragDropRemoved != 1 || *dragMotionRemoved != 1) {
		return -5;
	}
	g_object_set_data(G_OBJECT(view), marker, GINT_TO_POINTER(1));
	return 0;
}

// ---------------------------------------------------------------------------
// Native FileList bridge
// ---------------------------------------------------------------------------

#define FLOWDROP_DROP_MAX_FILES 64
// The already verified candidate: trigger the hidden input click shortly after
// the GTK drop callback returned, instead of inside GTK's nested drag loop.
#define FLOWDROP_DROP_IMPORT_DELAY_MS 300
// A drop that no chooser request consumed must not survive to answer a later
// file dialog.
#define FLOWDROP_DROP_PENDING_TIMEOUT_MS 3000
// Upper bound on how long an accepted drop may wait for its drag data.
#define FLOWDROP_DROP_DATA_TIMEOUT_US (5 * G_TIME_SPAN_SECOND)
// Coalesces hover evaluations so no JS runs inside the drag-motion emission.
#define FLOWDROP_DROP_HOVER_DELAY_MS 50

#define FLOWDROP_DROP_URI_TARGET "text/uri-list"
#define FLOWDROP_DROP_HTML_TARGET "text/html"
#define FLOWDROP_DROP_VIEW_MARKER "flowdrop-inbox-file-drop-bridge"

typedef struct {
	GtkWidget *view;      // borrowed; cleared by the "destroy" handler
	gchar *hubOrigin;     // owned copy of the one origin drops may come from

	// One drop batch. Only filled from an authorized drag-data-received.
	gchar *files[FLOWDROP_DROP_MAX_FILES + 1]; // NULL terminated for select_files
	guint fileCount;
	gchar *directoryHints[FLOWDROP_DROP_MAX_FILES]; // escaped basenames only
	guint directoryCount;
	gchar *token; // current per-drop accept MIME

	flowdrop_drop_auth auth; // one accepted drop waiting for its data
	guint importTimer;
	guint expireTimer;
	guint hoverTimer;
	gboolean hoverDesired;
	gboolean hoverSent;

	// Counters only: logs must never contain a local path or credential.
	guint droppedFiles;
	guint droppedDirectories;
	guint rejectedUris;
	guint importRefused;
	guint importFailed;
	guint chooserSelected;
	guint chooserCancelled;
	guint chooserDefault;
	guint batchesExpired;
	guint dropRefusedOrigin;
	guint dropRefusedMime;
	guint dropRefusedAction;
	guint dataExpired;
} flowdrop_drop_state;

static flowdrop_drop_state flowdropDrop;

static gboolean flowdrop_drop_origin_matches(void) {
	if (flowdropDrop.view == NULL || flowdropDrop.hubOrigin == NULL) {
		return FALSE;
	}
	const gchar *uri = webkit_web_view_get_uri(WEBKIT_WEB_VIEW(flowdropDrop.view));
	if (uri == NULL) {
		return FALSE;
	}
	gsize originLen = strlen(flowdropDrop.hubOrigin);
	if (originLen == 0 || strncmp(uri, flowdropDrop.hubOrigin, originLen) != 0) {
		return FALSE;
	}
	return uri[originLen] == '\0' || uri[originLen] == '/';
}

static void flowdrop_drop_eval(const gchar *script, GAsyncReadyCallback callback) {
	if (flowdropDrop.view == NULL || script == NULL) {
		return;
	}
	// Main world, exactly like the Wails-injected bridge script; nothing here
	// goes through the Wails runtime (the Hub origin is not the asset server).
	webkit_web_view_evaluate_javascript(WEBKIT_WEB_VIEW(flowdropDrop.view),
		script, -1, NULL, NULL, NULL, callback, NULL);
}

static void flowdrop_drop_clear_batch(void) {
	for (guint i = 0; i < flowdropDrop.fileCount; i++) {
		g_free(flowdropDrop.files[i]);
		flowdropDrop.files[i] = NULL;
	}
	flowdropDrop.fileCount = 0;
	flowdropDrop.files[0] = NULL;
	for (guint i = 0; i < flowdropDrop.directoryCount; i++) {
		g_free(flowdropDrop.directoryHints[i]);
		flowdropDrop.directoryHints[i] = NULL;
	}
	flowdropDrop.directoryCount = 0;
	g_clear_pointer(&flowdropDrop.token, g_free);
	flowdrop_drop_auth_clear(&flowdropDrop.auth);
	if (flowdropDrop.importTimer != 0) {
		g_source_remove(flowdropDrop.importTimer);
		flowdropDrop.importTimer = 0;
	}
	if (flowdropDrop.expireTimer != 0) {
		g_source_remove(flowdropDrop.expireTimer);
		flowdropDrop.expireTimer = 0;
	}
}

static gchar *flowdrop_drop_new_token(void) {
	// 64 random bits, ASCII only: a scoping marker for one drop batch, not a
	// secret. The MIME prefix keeps it distinguishable from page input types.
	return g_strdup_printf(FLOWDROP_DROP_TOKEN_PREFIX "%08x%08x",
		g_random_int(), g_random_int());
}

// flowdrop_drop_directory_hint returns a percent-encoded basename. The original
// path stays in C: JavaScript only ever receives a decodeURIComponent-able hint
// (safe for quotes, spaces and CJK names).
static gchar *flowdrop_drop_directory_hint(const gchar *path) {
	gchar *base = g_path_get_basename(path);
	gchar *escaped = g_uri_escape_string(base, NULL, TRUE);
	g_free(base);
	return escaped;
}

static void flowdrop_drop_import_done(GObject *object, GAsyncResult *result, gpointer userData) {
	(void)userData;
	GError *error = NULL;
	JSCValue *value = webkit_web_view_evaluate_javascript_finish(WEBKIT_WEB_VIEW(object), result, &error);
	if (value == NULL) {
		flowdropDrop.importFailed++;
		g_printerr("flowdrop inbox file drop: import bridge call failed (%s); no chooser opened (count=%u)\n",
			error != NULL ? error->message : "unknown", flowdropDrop.importFailed);
		g_clear_error(&error);
		flowdrop_drop_clear_batch();
		return;
	}
	gboolean accepted = jsc_value_is_boolean(value) && jsc_value_to_boolean(value);
	g_object_unref(value);
	if (!accepted) {
		flowdropDrop.importRefused++;
		g_printerr("flowdrop inbox file drop: page is not ready for a desktop drop (data-flowdrop-drop-ready != true or bridge missing); no chooser opened (count=%u)\n",
			flowdropDrop.importRefused);
		flowdrop_drop_clear_batch();
		return;
	}
	if (flowdropDrop.fileCount == 0) {
		// Directory-only drop: already dispatched to the page, nothing to
		// answer through a file chooser.
		flowdrop_drop_clear_batch();
	}
}

static gboolean flowdrop_drop_trigger_import(gpointer userData) {
	(void)userData;
	flowdropDrop.importTimer = 0;
	if (flowdropDrop.token == NULL) {
		return G_SOURCE_REMOVE;
	}
	if (!flowdrop_drop_origin_matches()) {
		flowdropDrop.batchesExpired++;
		g_printerr("flowdrop inbox file drop: page origin changed before the drop could be handed over; batch discarded (count=%u)\n",
			flowdropDrop.batchesExpired);
		flowdrop_drop_clear_batch();
		return G_SOURCE_REMOVE;
	}
	// Argument serialization: the token is ASCII hex, the directory hints are
	// already percent-encoded, so no path content can end a JS string literal.
	GString *script = g_string_new("window.__flowdropDesktopFileDropImport ? window.__flowdropDesktopFileDropImport(\"");
	g_string_append(script, flowdropDrop.token);
	g_string_append(script, "\", [");
	for (guint i = 0; i < flowdropDrop.directoryCount; i++) {
		if (i > 0) {
			g_string_append_c(script, ',');
		}
		g_string_append_c(script, '"');
		g_string_append(script, flowdropDrop.directoryHints[i]);
		g_string_append_c(script, '"');
	}
	g_string_append_printf(script, "], %s) : false;",
		flowdropDrop.fileCount > 0 ? "true" : "false");
	flowdrop_drop_eval(script->str, flowdrop_drop_import_done);
	g_string_free(script, TRUE);
	return G_SOURCE_REMOVE;
}

static gboolean flowdrop_drop_expire(gpointer userData) {
	(void)userData;
	flowdropDrop.expireTimer = 0;
	if (flowdropDrop.token == NULL) {
		return G_SOURCE_REMOVE;
	}
	flowdropDrop.batchesExpired++;
	g_printerr("flowdrop inbox file drop: dropped files were not consumed by the page file chooser in time; pending batch discarded (count=%u)\n",
		flowdropDrop.batchesExpired);
	flowdrop_drop_clear_batch();
	return G_SOURCE_REMOVE;
}

static GtkTargetList *flowdrop_drop_uri_targets(void) {
	static GtkTargetList *list = NULL;
	if (list == NULL) {
		list = gtk_target_list_new(NULL, 0);
		gtk_target_list_add(list, gdk_atom_intern_static_string(FLOWDROP_DROP_URI_TARGET), 0, 0);
	}
	return list;
}

static gboolean flowdrop_drop_offers(GdkDragContext *context, const gchar *targetName) {
	GdkAtom atom = gdk_atom_intern_static_string(targetName);
	for (GList *item = gdk_drag_context_list_targets(context); item != NULL; item = item->next) {
		if ((GdkAtom)item->data == atom) {
			return TRUE;
		}
	}
	return FALSE;
}

// flowdrop_drop_offers_copy reports whether the drag source itself lists COPY.
// The bridge only ever advertises an action the source offers.
static gboolean flowdrop_drop_offers_copy(GdkDragContext *context) {
	if (context == NULL) {
		return FALSE;
	}
	return (gdk_drag_context_get_actions(context) & GDK_ACTION_COPY) != 0;
}

static gboolean flowdrop_drop_is_file_drag(GtkWidget *widget, GdkDragContext *context) {
	if (context == NULL) {
		return FALSE;
	}
	if (gtk_drag_dest_find_target(widget, context, flowdrop_drop_uri_targets()) == GDK_NONE) {
		return FALSE;
	}
	// A browser link/text drag also offers text/uri-list (the URL) together
	// with text/html. Those keep WebKit's original handling; file managers do
	// not offer text/html for local files.
	return !flowdrop_drop_offers(context, FLOWDROP_DROP_HTML_TARGET);
}

static gboolean flowdrop_drop_hover_flush(gpointer userData) {
	(void)userData;
	flowdropDrop.hoverTimer = 0;
	if (!flowdrop_drop_origin_matches()) {
		// Never dispatch page events into a document that is not the Hub.
		flowdropDrop.hoverDesired = FALSE;
		flowdropDrop.hoverSent = FALSE;
		return G_SOURCE_REMOVE;
	}
	if (flowdropDrop.hoverDesired == flowdropDrop.hoverSent) {
		return G_SOURCE_REMOVE;
	}
	flowdropDrop.hoverSent = flowdropDrop.hoverDesired;
	flowdrop_drop_eval(flowdropDrop.hoverSent
			? "window.__flowdropDesktopDrag && window.__flowdropDesktopDrag(true);"
			: "window.__flowdropDesktopDrag && window.__flowdropDesktopDrag(false);",
		NULL);
	return G_SOURCE_REMOVE;
}

static void flowdrop_drop_set_hover(gboolean active) {
	if (flowdropDrop.hoverDesired == active) {
		return;
	}
	flowdropDrop.hoverDesired = active;
	if (flowdropDrop.hoverTimer == 0) {
		flowdropDrop.hoverTimer = g_timeout_add(FLOWDROP_DROP_HOVER_DELAY_MS, flowdrop_drop_hover_flush, NULL);
	}
}

// flowdrop_drop_log_refusal records why a drop was left to WebKit. One line per
// drop, reason category only: no path, URI, page content or credential, and
// nothing per drag-motion.
static void flowdrop_drop_log_refusal(gboolean originMatches, gboolean isFileDrag, gboolean sourceOffersCopy) {
	if (!originMatches) {
		g_printerr("flowdrop inbox file drop: drop left to WebKit (page is not the Hub origin, count=%u)\n",
			++flowdropDrop.dropRefusedOrigin);
		return;
	}
	if (!isFileDrag) {
		g_printerr("flowdrop inbox file drop: drop left to WebKit (not a local-file drag: no text/uri-list, or text/html is offered, count=%u)\n",
			++flowdropDrop.dropRefusedMime);
		return;
	}
	g_printerr("flowdrop inbox file drop: drop left to WebKit (source does not offer GDK_ACTION_COPY, count=%u)\n",
		++flowdropDrop.dropRefusedAction);
}

// flowdrop_drop_on_drag_drop accepts the drop only for a URI-list drag of local
// files on the current Hub origin whose source offers COPY, i.e. exactly the
// drags "drag-motion" claimed. Returning TRUE takes the drop away from WebKit,
// which is what stops the default "navigate to file:///..." action; everything
// else (text drags, browser link drags, foreign origins) returns FALSE so WebKit
// keeps its normal behaviour.
static gboolean flowdrop_drop_on_drag_drop(GtkWidget *widget, GdkDragContext *context,
	gint x, gint y, guint time, gpointer userData) {
	(void)x;
	(void)y;
	(void)userData;
	// A new drop revokes anything an earlier drop left behind.
	flowdrop_drop_auth_clear(&flowdropDrop.auth);
	gboolean originMatches = flowdrop_drop_origin_matches();
	if (context == NULL) {
		flowdrop_drop_log_refusal(originMatches, FALSE, FALSE);
		return FALSE;
	}
	gboolean isFileDrag = flowdrop_drop_is_file_drag(widget, context);
	gboolean sourceOffersCopy = flowdrop_drop_offers_copy(context);
	if (!flowdrop_drop_should_claim(originMatches, isFileDrag, sourceOffersCopy)) {
		flowdrop_drop_log_refusal(originMatches, isFileDrag, sourceOffersCopy);
		return FALSE;
	}
	flowdrop_drop_auth_accept(&flowdropDrop.auth, context, g_get_monotonic_time());
	gtk_drag_get_data(widget, context, gdk_atom_intern_static_string(FLOWDROP_DROP_URI_TARGET), time);
	return TRUE;
}

static void flowdrop_drop_on_drag_data_received(GtkWidget *widget, GdkDragContext *context,
	gint x, gint y, GtkSelectionData *data, guint info, guint time, gpointer userData) {
	(void)x;
	(void)y;
	(void)info;
	(void)userData;
	switch (flowdrop_drop_auth_data(&flowdropDrop.auth, context, g_get_monotonic_time(),
		FLOWDROP_DROP_DATA_TIMEOUT_US)) {
	case FLOWDROP_DROP_DATA_IGNORE:
		// Not the drop this code accepted (WebKit's own hover read, or a drag
		// that was left to WebKit). Leave it to WebKit's handler untouched.
		return;
	case FLOWDROP_DROP_DATA_EXPIRED:
		g_printerr("flowdrop inbox file drop: data arrived after the accepted drop's data window closed; ignored (count=%u)\n",
			++flowdropDrop.dataExpired);
		return;
	case FLOWDROP_DROP_DATA_CONSUME:
		break;
	}
	// This drop is ours: stop WebKit's own handler for it and finish the drag
	// exactly once, so nothing navigates and the source is released.
	g_signal_stop_emission_by_name(G_OBJECT(widget), "drag-data-received");
	gtk_drag_finish(context, TRUE, FALSE, time);
	flowdrop_drop_clear_batch();
	flowdrop_drop_set_hover(FALSE);

	gchar **uris = gtk_selection_data_get_uris(data);
	if (uris == NULL) {
		g_printerr("flowdrop inbox file drop: drop carried no URI list; ignored (count=%u)\n", ++flowdropDrop.rejectedUris);
		return;
	}
	for (gchar **uri = uris; *uri != NULL; uri++) {
		gchar *path = g_filename_from_uri(*uri, NULL, NULL);
		if (path == NULL) {
			// Non-local URI: never turned into a path, never selected.
			flowdropDrop.rejectedUris++;
			continue;
		}
		if (g_file_test(path, G_FILE_TEST_IS_DIR)) {
			if (flowdropDrop.directoryCount < FLOWDROP_DROP_MAX_FILES) {
				flowdropDrop.directoryHints[flowdropDrop.directoryCount++] =
					flowdrop_drop_directory_hint(path);
				flowdropDrop.droppedDirectories++;
			} else {
				flowdropDrop.rejectedUris++;
			}
		} else if (g_file_test(path, G_FILE_TEST_IS_REGULAR)) {
			if (flowdropDrop.fileCount < FLOWDROP_DROP_MAX_FILES) {
				flowdropDrop.files[flowdropDrop.fileCount++] = g_strdup(path);
				flowdropDrop.files[flowdropDrop.fileCount] = NULL;
				flowdropDrop.droppedFiles++;
			} else {
				flowdropDrop.rejectedUris++;
			}
		} else {
			flowdropDrop.rejectedUris++;
		}
		g_free(path);
	}
	g_strfreev(uris);

	if (flowdropDrop.fileCount == 0 && flowdropDrop.directoryCount == 0) {
		g_printerr("flowdrop inbox file drop: drop had no local files or directories; ignored (rejected URIs=%u)\n",
			flowdropDrop.rejectedUris);
		return;
	}
	flowdropDrop.token = flowdrop_drop_new_token();
	flowdropDrop.importTimer = g_timeout_add(FLOWDROP_DROP_IMPORT_DELAY_MS, flowdrop_drop_trigger_import, NULL);
	flowdropDrop.expireTimer = g_timeout_add(FLOWDROP_DROP_PENDING_TIMEOUT_MS, flowdrop_drop_expire, NULL);
	g_printerr("flowdrop inbox file drop: native drop accepted (files=%u dirs=%u rejected=%u)\n",
		flowdropDrop.fileCount, flowdropDrop.directoryCount, flowdropDrop.rejectedUris);
}

static gboolean flowdrop_drop_on_drag_motion(GtkWidget *widget, GdkDragContext *context,
	gint x, gint y, guint time, gpointer userData) {
	(void)x;
	(void)y;
	(void)userData;
	gboolean originMatches = flowdrop_drop_origin_matches();
	gboolean isFileDrag = context != NULL && flowdrop_drop_is_file_drag(widget, context);
	flowdrop_drop_set_hover(originMatches && isFileDrag);
	if (!flowdrop_drop_should_claim(originMatches, isFileDrag, flowdrop_drop_offers_copy(context))) {
		// Not this bridge's drag: never consume drag-motion and never touch the
		// action, so WebKit negotiates it exactly as before.
		return FALSE;
	}
	// This bridge consumes the drop itself, so it must negotiate the action too.
	// Returning TRUE stops WebKit's connect_after handler from overwriting COPY
	// with the page's drop operation (normally DragOperation::None, which makes
	// GDK Wayland accept no target at all).
	gdk_drag_status(context, GDK_ACTION_COPY, time);
	return TRUE;
}

static void flowdrop_drop_on_drag_leave(GtkWidget *widget, GdkDragContext *context, guint time, gpointer userData) {
	(void)widget;
	(void)context;
	(void)time;
	(void)userData;
	// Only the hover goes away here: an accepted drop keeps its authorization
	// until its data is consumed, its data window closes, the next drop, a page
	// load or view destroy (see filedrop_drop_cgo.h).
	flowdrop_drop_auth_leave(&flowdropDrop.auth);
	flowdrop_drop_set_hover(FALSE);
}

static gboolean flowdrop_drop_on_run_file_chooser(WebKitWebView *view, WebKitFileChooserRequest *request,
	gpointer userData) {
	(void)view;
	(void)userData;
	const gchar *const *mimeTypes = webkit_file_chooser_request_get_mime_types(request);
	switch (flowdrop_chooser_decision(mimeTypes, flowdropDrop.token)) {
	case FLOWDROP_CHOOSER_SELECT_DROP:
		if (flowdropDrop.fileCount == 0) {
			// A token without files cannot be answered with paths; cancel so
			// no dialog is opened for a drop that has nothing to hand over.
			webkit_file_chooser_request_cancel(request);
			flowdropDrop.chooserCancelled++;
			g_printerr("flowdrop inbox file drop: chooser request matched the current drop but no files were pending; cancelled (count=%u)\n",
				flowdropDrop.chooserCancelled);
			flowdrop_drop_clear_batch();
			return TRUE;
		}
		// WebKit copies the strings, so the batch can be cleared right after.
		webkit_file_chooser_request_select_files(request, (const gchar *const *)flowdropDrop.files);
		flowdropDrop.chooserSelected++;
		g_printerr("flowdrop inbox file drop: drop batch handed to the page file input (files=%u, count=%u)\n",
			flowdropDrop.fileCount, flowdropDrop.chooserSelected);
		flowdrop_drop_clear_batch();
		return TRUE;
	case FLOWDROP_CHOOSER_CANCEL_STALE:
		// A file chooser request from an earlier/expired drop batch: cancel it
		// instead of opening an ordinary dialog nobody asked for.
		webkit_file_chooser_request_cancel(request);
		flowdropDrop.chooserCancelled++;
		g_printerr("flowdrop inbox file drop: stale drop-token chooser request cancelled (count=%u)\n",
			flowdropDrop.chooserCancelled);
		return TRUE;
	default:
		flowdropDrop.chooserDefault++;
		// An ordinary page file input: WebKit's default handler opens the
		// normal file dialog, exactly as before this bridge existed.
		return FALSE;
	}
}

static void flowdrop_drop_on_load_changed(WebKitWebView *view, WebKitLoadEvent loadEvent, gpointer userData) {
	(void)userData;
	if (loadEvent != WEBKIT_LOAD_STARTED) {
		return;
	}
	if (flowdropDrop.view != GTK_WIDGET(view)) {
		return;
	}
	// A new document throws away the page-side bridge state; nothing pending
	// from the previous document may survive into it.
	flowdrop_drop_clear_batch();
	flowdropDrop.hoverDesired = FALSE;
	flowdropDrop.hoverSent = FALSE;
	if (flowdropDrop.hoverTimer != 0) {
		g_source_remove(flowdropDrop.hoverTimer);
		flowdropDrop.hoverTimer = 0;
	}
}

static void flowdrop_drop_on_view_destroy(GtkWidget *widget, gpointer userData) {
	(void)userData;
	if (flowdropDrop.view != widget) {
		return;
	}
	flowdrop_drop_clear_batch();
	if (flowdropDrop.hoverTimer != 0) {
		g_source_remove(flowdropDrop.hoverTimer);
		flowdropDrop.hoverTimer = 0;
	}
	flowdropDrop.hoverDesired = FALSE;
	flowdropDrop.hoverSent = FALSE;
	flowdropDrop.view = NULL;
}

// flowdrop_install_inbox_drop_bridge attaches the native drop receiver and the
// chooser interceptor to the inbox WebView and (re)sets the only origin a drop
// may come from. Idempotent per view: the handlers are connected once.
//
// Return: 0 installed/already installed, -1 no window, -2 no WebView.
static int flowdrop_install_inbox_drop_bridge(GtkWidget *window, const gchar *hubOrigin, int *newlyInstalled) {
	*newlyInstalled = 0;
	if (window == NULL) {
		return -1;
	}
	GtkWidget *view = flowdrop_find_webview(window, 0);
	if (view == NULL) {
		return -2;
	}
	if (flowdropDrop.view != view) {
		// A different view: drop any state that belonged to the previous one.
		flowdrop_drop_clear_batch();
	}
	flowdropDrop.view = view;
	g_free(flowdropDrop.hubOrigin);
	flowdropDrop.hubOrigin = g_strdup(hubOrigin);
	if (g_object_get_data(G_OBJECT(view), FLOWDROP_DROP_VIEW_MARKER) != NULL) {
		return 0;
	}
	g_signal_connect_data(view, "drag-drop", G_CALLBACK(flowdrop_drop_on_drag_drop), NULL, NULL, 0);
	g_signal_connect_data(view, "drag-motion", G_CALLBACK(flowdrop_drop_on_drag_motion), NULL, NULL, 0);
	g_signal_connect_data(view, "drag-leave", G_CALLBACK(flowdrop_drop_on_drag_leave), NULL, NULL, 0);
	g_signal_connect_data(view, "drag-data-received", G_CALLBACK(flowdrop_drop_on_drag_data_received), NULL, NULL, 0);
	g_signal_connect_data(view, "run-file-chooser", G_CALLBACK(flowdrop_drop_on_run_file_chooser), NULL, NULL, 0);
	g_signal_connect_data(view, "load-changed", G_CALLBACK(flowdrop_drop_on_load_changed), NULL, NULL, 0);
	g_signal_connect_data(view, "destroy", G_CALLBACK(flowdrop_drop_on_view_destroy), NULL, NULL, 0);
	g_object_set_data(G_OBJECT(view), FLOWDROP_DROP_VIEW_MARKER, GINT_TO_POINTER(1));
	*newlyInstalled = 1;
	return 0;
}
*/
import "C"

import (
	"log"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	inboxFileDropRelaxed    = 0
	inboxFileDropNoWork     = 1
	inboxFileDropNoWindow   = -1
	inboxFileDropNoView     = -2
	inboxFileDropNoSignal   = -3
	inboxFileDropAmbiguous  = -4
	inboxFileDropMismatched = -5
)

// relaxInboxFileDrop restores WebKit's default HTML5 file drag and drop for the
// inbox window only, by removing Wails' two GTK blockers on that window's
// WebView, and installs the native FileList bridge that hands the dropped local
// files to the page as a real FileList. It must run on the GTK main thread (see
// installInboxFileDropRelax); hubOrigin is the only origin a drop may come from
// and is refreshed on every finished page load.
//
// Every failure is logged and leaves the window exactly as it was: the drag
// still gets blocked (current behaviour) rather than panicking or touching some
// other window. WebviewWindowOptions.Linux.EnableFileDrop stays false, so Wails
// installs nothing new on the next window creation.
func relaxInboxFileDrop(win *application.WebviewWindow, hubOrigin string) {
	if win == nil {
		return
	}
	native := win.NativeWindow()
	if native == nil {
		log.Printf("inbox file drop: native GTK window not available yet; keeping Wails' drag-and-drop block")
		return
	}
	var dragDropRemoved, dragMotionRemoved C.int
	code := int(C.flowdrop_relax_inbox_dnd(
		(*C.GtkWidget)(native),
		C.uintptr_t(win.ID()),
		&dragDropRemoved,
		&dragMotionRemoved,
	))
	switch code {
	case inboxFileDropRelaxed:
		log.Printf("inbox file drop: WebKit HTML5 file drop restored (removed drag-drop=%d, drag-motion=%d)",
			int(dragDropRemoved), int(dragMotionRemoved))
	case inboxFileDropNoWork:
		log.Printf("inbox file drop: this view was already relaxed earlier in this process; nothing removed")
	case inboxFileDropNoView:
		log.Printf("inbox file drop: no WebKitWebView found in the inbox window tree; drag-and-drop block left in place")
	case inboxFileDropNoSignal:
		log.Printf("inbox file drop: GTK drag-drop/drag-motion signals not found; drag-and-drop block left in place")
	case inboxFileDropAmbiguous:
		log.Printf("inbox file drop: unexpected handler count for the inbox window (need exactly one drag-drop and one drag-motion); nothing removed")
	case inboxFileDropMismatched:
		log.Printf("inbox file drop: disconnect count mismatched (drag-drop=%d, drag-motion=%d); inspect before relying on file drop",
			int(dragDropRemoved), int(dragMotionRemoved))
	case inboxFileDropNoWindow:
		log.Printf("inbox file drop: no GTK window to inspect; drag-and-drop block left in place")
	}
	if code != inboxFileDropRelaxed && code != inboxFileDropNoWork {
		// Without the default WebKit drop restored there is no "drag-data-received"
		// to authorize, so the bridge cannot work; report instead of installing.
		return
	}
	installInboxFileDropBridge(native, hubOrigin)
}

// installInboxFileDropBridge installs (or re-arms) the native drop receiver on
// the inbox WebView. Only the origin matters here: the bridge never selects a
// path by anything the page says, and no path is exposed to JavaScript.
func installInboxFileDropBridge(native unsafe.Pointer, hubOrigin string) {
	origin := C.CString(hubOrigin)
	defer C.free(unsafe.Pointer(origin))
	var newlyInstalled C.int
	switch code := int(C.flowdrop_install_inbox_drop_bridge(
		(*C.GtkWidget)(native), origin, &newlyInstalled)); code {
	case 0:
		if int(newlyInstalled) == 1 {
			log.Printf("inbox file drop: native FileList bridge installed (origin=%s)", hubOrigin)
		} else {
			log.Printf("inbox file drop: native FileList bridge already installed; origin refreshed (%s)", hubOrigin)
		}
	case -1:
		log.Printf("inbox file drop: native FileList bridge not installed (no GTK window)")
	case -2:
		log.Printf("inbox file drop: native FileList bridge not installed (no WebKitWebView in the inbox window tree)")
	}
}
