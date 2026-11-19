//go:build linux && cgo && gtk3 && flowdrop_filedrop_probe

package main

/*
#cgo pkg-config: gobject-2.0

#include <glib-object.h>

#include "filedrop_signals_cgo.h"
#include "filedrop_chooser_cgo.h"
#include "filedrop_drop_cgo.h"

// This wrapper exists only in builds with the flowdrop_filedrop_probe tag and
// is used by the no-GUI regression test. It needs GLib/GObject only: a plain
// GObject, a test signal registered on G_TYPE_OBJECT and two C callbacks. No
// GTK init, no display, no window, no main loop, nothing that touches the
// user's desktop.

// A private GObject subclass is required: a signal can only be added while a
// type's class is being initialised, never to the already-initialised G_TYPE_OBJECT.
typedef struct { GObject parent; } FlowdropProbeObject;
typedef struct { GObjectClass parent_class; } FlowdropProbeObjectClass;

G_DEFINE_TYPE(FlowdropProbeObject, flowdrop_probe_object, G_TYPE_OBJECT)

static void flowdrop_probe_object_class_init(FlowdropProbeObjectClass *klass) {
	g_signal_new("flowdrop-probe-signal",
		G_TYPE_FROM_CLASS(klass),
		G_SIGNAL_RUN_LAST,
		0, NULL, NULL, NULL,
		G_TYPE_NONE, 0);
}

static void flowdrop_probe_object_init(FlowdropProbeObject *object) {
	(void)object;
}

static int flowdrop_probe_hits_a = 0;
static int flowdrop_probe_hits_b = 0;

static void flowdrop_probe_cb_a(GObject *object, gpointer data) {
	(void)object;
	(void)data;
	flowdrop_probe_hits_a++;
}

static void flowdrop_probe_cb_b(GObject *object, gpointer data) {
	(void)object;
	(void)data;
	flowdrop_probe_hits_b++;
}

static GObject *flowdrop_probe_new_object(void) {
	return G_OBJECT(g_object_new(flowdrop_probe_object_get_type(), NULL));
}

static void flowdrop_probe_connect(uintptr_t object, uintptr_t data, int which) {
	GCallback callback = which == 0 ? G_CALLBACK(flowdrop_probe_cb_a) : G_CALLBACK(flowdrop_probe_cb_b);
	g_signal_connect(G_OBJECT(object), "flowdrop-probe-signal", callback, (gpointer)data);
}

// flowdrop_probe_emit runs the remaining handlers; used to prove that counting
// (block + unblock) leaves every handler callable again.
static void flowdrop_probe_emit(uintptr_t object) {
	g_signal_emit_by_name(G_OBJECT(object), "flowdrop-probe-signal");
}

static int flowdrop_probe_hits(int which) {
	return which == 0 ? flowdrop_probe_hits_a : flowdrop_probe_hits_b;
}

static void flowdrop_probe_reset_hits(void) {
	flowdrop_probe_hits_a = 0;
	flowdrop_probe_hits_b = 0;
}

// flowdrop_probe_match_count calls the exact production helper with the signal
// id looked up from its name, like flowdrop_relax_inbox_dnd does.
static int flowdrop_probe_match_count(uintptr_t object, uintptr_t data) {
	GObject *obj = G_OBJECT(object);
	return flowdrop_match_count(obj, g_signal_lookup("flowdrop-probe-signal", G_OBJECT_TYPE(obj)), (gpointer)data);
}

static int flowdrop_probe_disconnect(uintptr_t object, uintptr_t data) {
	GObject *obj = G_OBJECT(object);
	return flowdrop_disconnect_matched(obj, g_signal_lookup("flowdrop-probe-signal", G_OBJECT_TYPE(obj)), (gpointer)data);
}

static void flowdrop_probe_unref(uintptr_t object) {
	g_object_unref(G_OBJECT(object));
}

// flowdrop_probe_chooser_decision runs the production run-file-chooser decision
// on a caller-supplied MIME list. Pure string logic: no GTK, no window, no
// display, so it is safe in a no-GUI test.
static int flowdrop_probe_chooser_decision(const gchar **mimeTypes, int count, const gchar *currentToken) {
	const gchar *list[9];
	int n = count > 8 ? 8 : count;
	for (int i = 0; i < n; i++) {
		list[i] = mimeTypes[i];
	}
	list[n] = NULL;
	return flowdrop_chooser_decision(list, currentToken);
}

// flowdrop_probe_token_prefix exposes the single source of truth (the C macro)
// so the no-GUI test can build realistic MIME lists without duplicating it.
static const gchar *flowdrop_probe_token_prefix(void) {
	return FLOWDROP_DROP_TOKEN_PREFIX;
}

// flowdrop_probe_chooser_code exposes the decision enum without duplicating
// the numeric values in Go: 0 default, 1 select, 2 cancel stale.
static int flowdrop_probe_chooser_code(int which) {
	switch (which) {
	case 0:
		return FLOWDROP_CHOOSER_DEFAULT;
	case 1:
		return FLOWDROP_CHOOSER_SELECT_DROP;
	default:
		return FLOWDROP_CHOOSER_CANCEL_STALE;
	}
}

// flowdrop_probe_should_claim runs the production drag-action rule.
static int flowdrop_probe_should_claim(int originMatches, int isFileDrag, int sourceOffersCopy) {
	return flowdrop_drop_should_claim(originMatches != 0, isFileDrag != 0, sourceOffersCopy != 0) ? 1 : 0;
}

// The drop-authorization state machine, driven exactly as
// filedrop_linux_gtk3.go drives it: accept on "drag-drop", leave on
// "drag-leave", data on "drag-data-received", clear on load/destroy. Only an
// integer stands in for the GdkDragContext address; it is never dereferenced.
static flowdrop_drop_auth flowdrop_probe_auth;

static void flowdrop_probe_auth_reset(void) {
	flowdrop_drop_auth_clear(&flowdrop_probe_auth);
}

static void flowdrop_probe_auth_accept(uintptr_t context, int64_t now) {
	flowdrop_drop_auth_accept(&flowdrop_probe_auth, (gpointer)context, now);
}

static void flowdrop_probe_auth_leave(void) {
	flowdrop_drop_auth_leave(&flowdrop_probe_auth);
}

static int flowdrop_probe_auth_data(uintptr_t context, int64_t now, int64_t timeoutUs) {
	return flowdrop_drop_auth_data(&flowdrop_probe_auth, (gpointer)context, now, timeoutUs);
}

static uintptr_t flowdrop_probe_auth_context(void) {
	return (uintptr_t)flowdrop_probe_auth.context;
}

static uintptr_t flowdrop_probe_data_code(int which) {
	switch (which) {
	case 0:
		return FLOWDROP_DROP_DATA_IGNORE;
	case 1:
		return FLOWDROP_DROP_DATA_EXPIRED;
	default:
		return FLOWDROP_DROP_DATA_CONSUME;
	}
}
*/
import "C"

import "unsafe"

// Wrappers for desktop/filedrop_signal_probe_test.go. user_data values are
// plain integers cast to gpointer, exactly like Wails passes the window id
// (the callbacks never dereference them).

func probeNewObject() uintptr { return uintptr(unsafe.Pointer(C.flowdrop_probe_new_object())) }

func probeConnect(object uintptr, data uintptr, which int) {
	C.flowdrop_probe_connect(C.uintptr_t(object), C.uintptr_t(data), C.int(which))
}

func probeEmit(object uintptr) { C.flowdrop_probe_emit(C.uintptr_t(object)) }

func probeHits(which int) int { return int(C.flowdrop_probe_hits(C.int(which))) }

func probeResetHits() { C.flowdrop_probe_reset_hits() }

func probeMatchCount(object uintptr, data uintptr) int {
	return int(C.flowdrop_probe_match_count(C.uintptr_t(object), C.uintptr_t(data)))
}

func probeDisconnect(object uintptr, data uintptr) int {
	return int(C.flowdrop_probe_disconnect(C.uintptr_t(object), C.uintptr_t(data)))
}

func probeUnref(object uintptr) { C.flowdrop_probe_unref(C.uintptr_t(object)) }

// probeChooserDecision runs the production MIME classification with a fixed
// token ("" means "no current drop token") and returns the decision code.
func probeChooserDecision(mimeTypes []string, currentToken string) int {
	const maxMimeTypes = 8
	var list [maxMimeTypes + 1]*C.gchar
	n := 0
	for _, mime := range mimeTypes {
		if n >= maxMimeTypes {
			break
		}
		c := C.CString(mime)
		defer C.free(unsafe.Pointer(c))
		list[n] = c
		n++
	}
	var token *C.gchar
	if currentToken != "" {
		c := C.CString(currentToken)
		defer C.free(unsafe.Pointer(c))
		token = c
	}
	return int(C.flowdrop_probe_chooser_decision(&list[0], C.int(n), token))
}

// probeDropTokenPrefix returns the per-drop accept MIME prefix from C.
func probeDropTokenPrefix() string { return C.GoString(C.flowdrop_probe_token_prefix()) }

// probeChooserCode returns the production decision enum value: 0 default,
// 1 select the pending drop, 2 cancel a stale drop request.
func probeChooserCode(which int) int { return int(C.flowdrop_probe_chooser_code(C.int(which))) }

// probeShouldClaim runs the production drag-action rule: claim COPY only for a
// Hub-origin local-file drag whose source actually offers COPY.
func probeShouldClaim(originMatches, isFileDrag, sourceOffersCopy bool) bool {
	return C.flowdrop_probe_should_claim(C.int(btoi(originMatches)), C.int(btoi(isFileDrag)), C.int(btoi(sourceOffersCopy))) == 1
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

// probeAuthReset clears the authorization, like a page load or view destroy.
func probeAuthReset() { C.flowdrop_probe_auth_reset() }

// probeAuthAccept authorizes one accepted drop for the given fake context.
func probeAuthAccept(context uintptr, now int64) {
	C.flowdrop_probe_auth_accept(C.uintptr_t(context), C.int64_t(now))
}

// probeAuthLeave runs the production "drag-leave" rule.
func probeAuthLeave() { C.flowdrop_probe_auth_leave() }

// probeAuthData runs the production "drag-data-received" authorization check.
func probeAuthData(context uintptr, now int64, timeoutUs int64) int {
	return int(C.flowdrop_probe_auth_data(C.uintptr_t(context), C.int64_t(now), C.int64_t(timeoutUs)))
}

// probeAuthContext returns the still-authorized context (0 when none).
func probeAuthContext() uintptr { return uintptr(C.flowdrop_probe_auth_context()) }

// probeDataCode returns the production data-decision enum value.
func probeDataCode(which int) int { return int(C.flowdrop_probe_data_code(C.int(which))) }
