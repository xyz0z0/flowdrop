//go:build linux && cgo && gtk3 && flowdrop_webview_proxy_probe

package main

/*
#cgo pkg-config: webkit2gtk-4.1

#include <gtk/gtk.h>
#include <webkit2/webkit2.h>
#include <stdlib.h>

static int flowdropProbeInit(void) {
	int argc = 1;
	char *args[] = {(char *)"flowdrop-webview-proxy-probe", NULL};
	char **argv = args;
	return gtk_init_check(&argc, &argv);
}

// flowdropProbeNewView deliberately mirrors Wails v3.1.0's gtk3 view
// construction (linux_cgo_gtk3.go) so the probe uses the same default
// WebKitWebContext / WebsiteDataManager as the real application.
static GtkWidget *flowdropProbeNewView(void) {
	WebKitUserContentManager *manager = webkit_user_content_manager_new();
	GtkWidget *view = webkit_web_view_new_with_user_content_manager(manager);
	g_object_unref(manager);
	return view;
}

static GtkWidget *flowdropProbeNewWindow(GtkWidget *view) {
	GtkWidget *window = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	gtk_container_add(GTK_CONTAINER(window), view);
	gtk_widget_show_all(window);
	return window;
}

static void flowdropProbeLoad(GtkWidget *view, const char *uri) {
	webkit_web_view_load_uri(WEBKIT_WEB_VIEW(view), uri);
}

static const char *flowdropProbeTitle(GtkWidget *view) {
	return webkit_web_view_get_title(WEBKIT_WEB_VIEW(view));
}

static gboolean flowdropProbeIterate(void) {
	return gtk_main_iteration_do(FALSE);
}

static void flowdropProbeDestroyWindow(GtkWidget *window) {
	gtk_widget_destroy(window);
}
*/
import "C"

import "unsafe"

// These wrappers exist only in builds with the flowdrop_webview_proxy_probe
// tag. The e2e test must not import "C" itself: the go tool rejects cgo in
// _test.go files. Normal desktop builds never compile this file.

func probeGTKInit() bool { return C.flowdropProbeInit() != 0 }

func probeNewView() uintptr { return uintptr(unsafe.Pointer(C.flowdropProbeNewView())) }

func probeNewWindow(view uintptr) uintptr {
	return uintptr(unsafe.Pointer(C.flowdropProbeNewWindow((*C.GtkWidget)(unsafe.Pointer(view)))))
}

func probeLoad(view uintptr, uri string) {
	cURI := C.CString(uri)
	defer C.free(unsafe.Pointer(cURI))
	C.flowdropProbeLoad((*C.GtkWidget)(unsafe.Pointer(view)), cURI)
}

func probeTitle(view uintptr) string {
	return C.GoString(C.flowdropProbeTitle((*C.GtkWidget)(unsafe.Pointer(view))))
}

func probeIterate() { C.flowdropProbeIterate() }

func probeDestroyWindow(window uintptr) {
	C.flowdropProbeDestroyWindow((*C.GtkWidget)(unsafe.Pointer(window)))
}
