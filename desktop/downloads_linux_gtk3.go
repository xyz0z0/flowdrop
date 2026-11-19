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

#define FLOWDROP_CONTEXT_MARKER "flowdrop-download-handler"
#define FLOWDROP_DOWNLOAD_MARKER "flowdrop-download-hooked"

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

// flowdrop_download_on_failed only reports. The outcome is decided in
// decide-destination; a failure after that means WebKit itself could not write
// the chosen path, and the user must see why instead of a silent no-op.
static void flowdrop_download_on_failed(WebKitDownload *download, GError *error, gpointer userData) {
	(void)download;
	(void)userData;
	// The GError message routinely embeds the local path it failed on, so only
	// the domain and code are logged: logs get copied into bug reports, paths do
	// not need to travel with them.
	g_printerr("flowdrop download: failed (domain=%u code=%d); no existing file was deleted or overwritten by FlowDrop\n",
		error != NULL ? error->domain : 0, error != NULL ? error->code : 0);
}

// flowdrop_download_on_decide_destination returns TRUE because FlowDrop claims
// the destination decision. Returning FALSE would hand the decision back to
// WebKit's default, i.e. exactly the behaviour that damages an existing file.
static gboolean flowdrop_download_on_decide_destination(WebKitDownload *download, gchar *suggestedFilename, gpointer userData) {
	(void)userData;
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

// flowdrop_download_on_started connects the per-download handlers. WebKit emits
// decide-destination once per download and expects an answer synchronously, so
// the handler has to be attached before that emission, i.e. here.
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
	g_signal_connect(download, "decide-destination", G_CALLBACK(flowdrop_download_on_decide_destination), NULL);
	g_signal_connect(download, "failed", G_CALLBACK(flowdrop_download_on_failed), NULL);
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
	"log"
	"os"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var downloadHandlerOnce sync.Once

// installDownloadHandler installs FlowDrop's WebKitGTK download destination
// handler exactly once per process. It only connects two signals on the default
// WebKitWebContext, so it can run before app.Run (before any window exists) and
// never creates a window or a GTK surface itself.
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
