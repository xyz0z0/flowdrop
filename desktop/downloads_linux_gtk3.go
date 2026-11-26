//go:build linux && cgo && gtk3

package main

/*
#cgo pkg-config: webkit2gtk-4.1

#include <glib.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>

// flowdropDownloadDestination is implemented in Go (below): it maps WebKit's
// untrusted suggested filename onto a free path inside the download directory.
extern char *flowdropDownloadDestination(char *suggestedFilename);

// The three exports below are also implemented in Go (below). They make this
// process the authority on "is this item already downloading/saved?" and report
// the real WebKit outcome to the page (downloads_track.go).
extern int flowdropDownloadBegin(char *uri, void *webView);
extern void flowdropDownloadFinished(char *uri, char *destination, void *webView);
extern void flowdropDownloadFailed(char *uri, void *webView);

// flowdrop_download_eval runs one script in the initiating WebView's main world.
// The download signal handlers run on the GTK main thread, so this is the same
// direct evaluation the file-drop bridge uses (filedrop_linux_gtk3.go): no Wails
// runtime, no window lookup, nothing crossing threads.
static void flowdrop_download_eval(WebKitWebView *view, const gchar *payload) {
	if (view == NULL || payload == NULL) {
		return;
	}
	gchar *script = g_strdup_printf("window.__flowdropDownload && window.__flowdropDownload(%s);", payload);
	webkit_web_view_evaluate_javascript(view, script, -1, NULL, NULL, NULL, NULL, NULL);
	g_free(script);
}

// flowdrop_download_view_origin_matches reports whether the view that started a
// download still shows a document on the same origin (the file-drop bridge uses
// the same rule). A download of the previous Hub finishing after a Hub switch is
// therefore never delivered into the new Hub's page.
static gboolean flowdrop_download_view_origin_matches(WebKitWebView *view, const gchar *origin) {
	if (view == NULL || origin == NULL || origin[0] == '\0') {
		return FALSE;
	}
	const gchar *uri = webkit_web_view_get_uri(view);
	if (uri == NULL) {
		return FALSE;
	}
	gsize originLen = strlen(origin);
	if (strncmp(uri, origin, originLen) != 0) {
		return FALSE;
	}
	return uri[originLen] == '\0' || uri[originLen] == '/';
}

#define FLOWDROP_CONTEXT_MARKER "flowdrop-download-handler"
#define FLOWDROP_DOWNLOAD_MARKER "flowdrop-download-hooked"
// FLOWDROP_DOWNLOAD_FAILED survives between the two terminal signals: WebKit
// emits "failed" before "finished" (WebKitDownload::failed docs), and only
// "finished" without this marker counts as success.
#define FLOWDROP_DOWNLOAD_TRACKED "flowdrop-download-tracked"
#define FLOWDROP_DOWNLOAD_FAILED "flowdrop-download-failed"

// Decisions returned by flowdropDownloadBegin, mirrored in downloads_track.go.
#define FLOWDROP_DOWNLOAD_UNTRACKED 0
#define FLOWDROP_DOWNLOAD_NEW 1
#define FLOWDROP_DOWNLOAD_DUPLICATE_PENDING 2
#define FLOWDROP_DOWNLOAD_DUPLICATE_DONE 3

// Why this file exists: Wails v3.1.0's gtk3 backend connects nothing to
// WebKitWebContext's "download-started" (its Linux module contains no download
// handling at all), so WebKitGTK's default decide-destination applies: save into
// G_USER_DIRECTORY_DOWNLOAD under the suggested filename with allow-overwrite
// FALSE. When that file already exists the download fails on top of the
// existing file, which is how the first click on "下载" destroyed the previous
// copy and only the second click produced a file.
//
// The fix is the documented WebKitGTK extension point: answer
// decide-destination for every download and hand WebKit a destination that is
// free right now. FlowDrop never deletes or truncates anything: the destination
// is chosen by looking for a name nothing occupies (download_naming.go).

// flowdrop_download_uri returns the request URI that started the download, or
// NULL. It is owned by the request, so it is only read, never freed.
static const gchar *flowdrop_download_uri(WebKitDownload *download) {
	WebKitURIRequest *request = webkit_download_get_request(download);
	return request != NULL ? webkit_uri_request_get_uri(request) : NULL;
}

// flowdrop_download_on_failed is connected to every non-duplicate download, so a
// failure is always logged (it used to be, before status tracking existed). Only
// a tracked inbox item also updates its record and tells the page: the record is
// dropped there, which makes the item retryable immediately.
static void flowdrop_download_on_failed(WebKitDownload *download, GError *error, gpointer userData) {
	(void)userData;
	if (g_object_get_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_FAILED) != NULL) {
		return;
	}
	g_object_set_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_FAILED, GINT_TO_POINTER(1));
	// The GError message routinely embeds the local path it failed on, so only
	// the domain and code are logged: logs get copied into bug reports, paths do
	// not need to travel with them.
	g_printerr("flowdrop download: failed (domain=%u code=%d); no existing file was deleted or overwritten by FlowDrop\n",
		error != NULL ? error->domain : 0, error != NULL ? error->code : 0);
	if (g_object_get_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_TRACKED) == NULL) {
		return;
	}
	const gchar *uri = flowdrop_download_uri(download);
	flowdropDownloadFailed(uri != NULL ? (char *)uri : "",
		webkit_download_get_web_view(download));
}

// flowdrop_download_on_finished is the only success signal: WebKit emits
// "finished" after "failed" too, so the failure marker decides which it is.
static void flowdrop_download_on_finished(WebKitDownload *download, gpointer userData) {
	(void)userData;
	if (g_object_get_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_FAILED) != NULL) {
		return;
	}
	const gchar *uri = flowdrop_download_uri(download);
	const gchar *destination = webkit_download_get_destination(download);
	flowdropDownloadFinished(uri != NULL ? (char *)uri : "",
		destination != NULL ? (char *)destination : "",
		webkit_download_get_web_view(download));
}

// flowdrop_download_on_decide_destination returns TRUE because FlowDrop claims
// the destination decision. Returning FALSE would hand the decision back to
// WebKit's default, i.e. exactly the behaviour that damages an existing file.
static gboolean flowdrop_download_on_decide_destination(WebKitDownload *download, gchar *suggestedFilename, gpointer userData) {
	(void)userData;
	// A tracked inbox item whose response carries a known, definite non-2xx
	// status (expired session, deleted item) would otherwise be written to disk
	// as if it were the file, and WebKit would still emit "finished". Refuse it
	// before any destination exists, report the failure to the page, and cancel
	// instead of saving. Untracked downloads and a status that is not known yet
	// (0) keep WebKit's existing behaviour.
	guint status = 0;
	WebKitURIResponse *response = webkit_download_get_response(download);
	if (response != NULL) {
		status = webkit_uri_response_get_status_code(response);
	}
	if (g_object_get_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_TRACKED) != NULL &&
		status >= 100 && (status < 200 || status >= 300)) {
		g_object_set_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_FAILED, GINT_TO_POINTER(1));
		g_printerr("flowdrop download: refused HTTP status %u; nothing was saved\n", status);
		const gchar *uri = flowdrop_download_uri(download);
		flowdropDownloadFailed(uri != NULL ? (char *)uri : "",
			webkit_download_get_web_view(download));
		webkit_download_cancel(download);
		return TRUE;
	}
	// The buffer is a plain malloc() string, so it is released with free().
	char *destination = flowdropDownloadDestination(suggestedFilename != NULL ? suggestedFilename : "");
	if (destination == NULL) {
		g_printerr("flowdrop download: no usable download directory; leaving this destination to WebKit\n");
		return FALSE;
	}
	// webkit_download_set_destination() is void in WebKitGTK 4.1: claiming the
	// decision is expressed by the TRUE return value below, and the destination
	// is logged by the failed handler if WebKit still cannot write it.
	webkit_download_set_destination(download, destination);
	free(destination);
	return TRUE;
}

// flowdrop_download_on_claimed_destination answers decide-destination for a
// duplicate that was already cancelled: it claims the decision without naming a
// destination, so WebKit's default (which would write into the user's download
// directory) can never run for a download FlowDrop already refused.
static gboolean flowdrop_download_on_claimed_destination(WebKitDownload *download, gchar *suggestedFilename, gpointer userData) {
	(void)download;
	(void)suggestedFilename;
	(void)userData;
	return TRUE;
}

// flowdrop_download_on_started connects the per-download handlers. WebKit emits
// decide-destination once per download and expects an answer synchronously, so
// the handler has to be attached before that emission, i.e. here.
//
// This is also where a repeat click is answered: the same Hub item that is still
// downloading, or whose saved file is still there, is cancelled here instead of
// being saved a second time (downloads_track.go owns that decision).
static void flowdrop_download_on_started(WebKitWebContext *context, WebKitDownload *download, gpointer userData) {
	(void)context;
	(void)userData;
	if (g_object_get_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_MARKER) != NULL) {
		return;
	}
	g_object_set_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_MARKER, GINT_TO_POINTER(1));
	// Stated explicitly even though FALSE is the WebKitGTK default: never let a
	// later WebKit default change turn a same-name download into an overwrite.
	webkit_download_set_allow_overwrite(download, FALSE);
	const gchar *uri = flowdrop_download_uri(download);
	int decision = flowdropDownloadBegin(uri != NULL ? (char *)uri : "",
		webkit_download_get_web_view(download));
	if (decision == FLOWDROP_DOWNLOAD_DUPLICATE_PENDING || decision == FLOWDROP_DOWNLOAD_DUPLICATE_DONE) {
		// Cancel before any destination is chosen, so no second file (and no
		// partial one) can appear for an item that is already handled.
		g_signal_connect(download, "decide-destination", G_CALLBACK(flowdrop_download_on_claimed_destination), NULL);
		webkit_download_cancel(download);
		return;
	}
	g_signal_connect(download, "decide-destination", G_CALLBACK(flowdrop_download_on_decide_destination), NULL);
	// Failure is logged for every download; only tracked inbox items also update
	// their record and report to the page.
	g_signal_connect(download, "failed", G_CALLBACK(flowdrop_download_on_failed), NULL);
	if (decision == FLOWDROP_DOWNLOAD_NEW) {
		g_object_set_data(G_OBJECT(download), FLOWDROP_DOWNLOAD_TRACKED, GINT_TO_POINTER(1));
		g_signal_connect(download, "finished", G_CALLBACK(flowdrop_download_on_finished), NULL);
	}
}

// flowdrop_install_download_handler hooks the process-wide default
// WebKitWebContext, which is the context Wails' gtk3 views use (see
// webview_proxy_linux_gtk3.go). Return: 1 installed now, 0 already installed,
// <0 no default context.
static int flowdrop_install_download_handler(void) {
	WebKitWebContext *context = webkit_web_context_get_default();
	if (context == NULL) {
		return -1;
	}
	if (g_object_get_data(G_OBJECT(context), FLOWDROP_CONTEXT_MARKER) != NULL) {
		return 0;
	}
	g_signal_connect(context, "download-started", G_CALLBACK(flowdrop_download_on_started), NULL);
	g_object_set_data(G_OBJECT(context), FLOWDROP_CONTEXT_MARKER, GINT_TO_POINTER(1));
	return 1;
}
*/
import "C"

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var downloadHandlerOnce sync.Once

// installDownloadHandler installs FlowDrop's WebKitGTK download handler exactly
// once per process. It connects one signal on the default WebKitWebContext (the
// per-download signals are connected from that handler), so it can run before
// app.Run (before any window exists) and never creates a window or a GTK surface
// itself.
//
// Threading: WebKit signals must be connected and delivered on the GTK main
// thread. application.InvokeSync cannot be used from the startup path: before
// app.Run there is no main loop yet, so Wails only queues the work in its
// earlyDispatch list and InvokeSync would then wait for a list that is drained
// from inside app.Run — a self-deadlock. application.InvokeAsync queues the same
// work without waiting; it runs on the GTK main thread on the loop's first idle
// turn, long before a page can ask for a download.
func installDownloadHandler() {
	downloadHandlerOnce.Do(func() {
		install := func() {
			switch code := int(C.flowdrop_install_download_handler()); code {
			case 1:
				log.Printf("downloads: WebKitGTK destination handler installed (a same-name download gets a new name, existing files are never overwritten)")
			case 0:
				log.Printf("downloads: WebKitGTK destination handler was already installed in this process; nothing changed")
			default:
				log.Printf("downloads: WebKitGTK default WebContext unavailable; downloads keep WebKit's own behaviour")
			}
		}
		if application.Get() == nil {
			// No Wails application (tests and probes): there is no main loop to
			// dispatch to, so the connect happens on the calling thread.
			install()
			return
		}
		application.InvokeAsync(install)
	})
}

// sendDownloadEvent hands one status to the WebView that started the download.
// It runs on the GTK main thread, inside the WebKit signal handler, so it
// evaluates directly on that view (the file-drop bridge's pattern): no Wails
// runtime, no window field read from a goroutine. The page only gets the item id
// and a status; the saved path stays in this process, and the tracker has already
// refused to report for another Hub origin than the one the inbox shows.
func sendDownloadEvent(view *C.WebKitWebView, origin string, ev downloadEvent) {
	if view == nil || origin == "" {
		return
	}
	// Only report into a view that still shows the origin the download belongs
	// to: a download of the previous Hub finishing after a Hub switch must not
	// surface in the new Hub's page.
	cOrigin := C.CString(origin)
	defer C.free(unsafe.Pointer(cOrigin))
	if C.flowdrop_download_view_origin_matches(view, cOrigin) == 0 {
		return
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	// The injected bridge (downloads_track.go, desktopDownloadStatusJS) only
	// forwards a fixed CustomEvent; it exposes no native method to the Hub page.
	jsonPayload := C.CString(string(payload))
	defer C.free(unsafe.Pointer(jsonPayload))
	C.flowdrop_download_eval(view, jsonPayload)
}

//export flowdropDownloadBegin
func flowdropDownloadBegin(uri *C.char, view unsafe.Pointer) C.int {
	if uri == nil {
		return C.int(int(downloadUntracked))
	}
	key, ok := parseDownloadRequest(C.GoString(uri))
	if !ok {
		return C.int(int(downloadUntracked))
	}
	decision, ev := processDownloads.begin(key)
	if decision != downloadUntracked {
		// begin only tracks the Hub origin the inbox shows right now.
		sendDownloadEvent((*C.WebKitWebView)(view), key.origin, ev)
	}
	return C.int(int(decision))
}

//export flowdropDownloadFinished
func flowdropDownloadFinished(uri *C.char, destination *C.char, view unsafe.Pointer) {
	if uri == nil {
		return
	}
	key, ok := parseDownloadRequest(C.GoString(uri))
	if !ok {
		return
	}
	dest := ""
	if destination != nil {
		dest = C.GoString(destination)
	}
	// The record is always updated; the page is told only while the inbox still
	// shows the Hub origin the download belongs to.
	if ev, updated, deliver := processDownloads.finish(key, dest); updated && deliver {
		sendDownloadEvent((*C.WebKitWebView)(view), key.origin, ev)
	}
}

//export flowdropDownloadFailed
func flowdropDownloadFailed(uri *C.char, view unsafe.Pointer) {
	if uri == nil {
		return
	}
	key, ok := parseDownloadRequest(C.GoString(uri))
	if !ok {
		return
	}
	if ev, updated, deliver := processDownloads.fail(key); updated && deliver {
		sendDownloadEvent((*C.WebKitWebView)(view), key.origin, ev)
	}
}

//export flowdropDownloadDestination
func flowdropDownloadDestination(suggestedFilename *C.char) *C.char {
	name := sanitizeDownloadName(C.GoString(suggestedFilename))
	special := ""
	if raw := C.g_get_user_special_dir(C.G_USER_DIRECTORY_DOWNLOAD); raw != nil {
		special = C.GoString(raw)
	}
	dir, err := resolveDownloadDir(special, os.Getenv("HOME"))
	if err != nil {
		log.Printf("downloads: no usable download directory: %v", err)
		return nil
	}
	// The suggested name is untrusted, so it is never logged: only the fact that
	// a free name was picked.
	path, err := uniqueDownloadPath(dir, name)
	if err != nil {
		log.Printf("downloads: could not pick a free file name in the download directory: %v", err)
		return nil
	}
	return C.CString(path)
}

// downloadStatusBridgeEnabled is true here: this build's WebKit context reports
// the real outcome of every inbox item download.
const downloadStatusBridgeEnabled = true
