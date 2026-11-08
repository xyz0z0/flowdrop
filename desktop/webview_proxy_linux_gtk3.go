//go:build linux && cgo && gtk3

package main

/*
#cgo pkg-config: webkit2gtk-4.1

#include <webkit2/webkit2.h>

// flowdropDefaultDataManager returns the WebKitGTK website data manager that
// the application's WebViews use. Wails v3.1.0 creates its LINUX gtk3 views
// with webkit_web_view_new_with_user_content_manager(), which (in WebKitGTK
// 4.1) uses the default WebKitWebContext and therefore this data manager.
static WebKitWebsiteDataManager *flowdropDefaultDataManager(void) {
	WebKitWebContext *context = webkit_web_context_get_default();
	if (context == NULL) {
		return NULL;
	}
	return webkit_web_context_get_website_data_manager(context);
}

static int flowdropApplyNoProxy(void) {
	WebKitWebsiteDataManager *manager = flowdropDefaultDataManager();
	if (manager == NULL) {
		return 0;
	}
	webkit_website_data_manager_set_network_proxy_settings(
		manager, WEBKIT_NETWORK_PROXY_MODE_NO_PROXY, NULL);
	return 1;
}
*/
import "C"

import "errors"

// configureDesktopWebViewProxy makes this process's WebKitGTK network stack
// ignore the host's HTTP proxy.
//
// Why this is safe and narrow:
//
//   - The FlowDrop desktop windows only load the app's own Hub origin and the
//     local wails:// asset server. There is no third-party WebView traffic to
//     protect; external links are handed to the system browser, which keeps
//     its normal proxy configuration.
//   - The setting lives in this process's WebKitWebsiteDataManager only. It
//     never writes kioslaverc/GSettings/environment variables and does not
//     change the system proxy for any other application.
//   - It works regardless of how the host proxy is configured (KDE
//     kioslaverc, GSettings, http_proxy, a transparent proxy, or no proxy at
//     all). It deliberately does not rely on no_proxy: GIO/libproxy on this
//     machine keeps returning the KDE proxy for LAN addresses even when the
//     environment's no_proxy names them.
//   - WEBKIT_NETWORK_PROXY_MODE_NO_PROXY covers every URL the WebView loads,
//     including ws:// and wss://, so the same logic keeps working when the Hub
//     is reached through a different interface (home LAN vs Tailscale) without
//     restarting the app.
//
// Call it once on the process main thread (Wails locks it in init), after
// application.New and before createWindows/app.Run. Creating the default
// WebKit context here is safe even before gtk_init: it does not create a
// window. WebKitGTK does not expose a getter for the applied mode, so a nil
// return means "the setting was accepted by WebKit", not "a later page has
// already proven the network path".
func configureDesktopWebViewProxy() error {
	if C.flowdropApplyNoProxy() == 0 {
		return errors.New("WebKitGTK 默认 WebContext/WebsiteDataManager 不可用，无法为本应用设置代理直连")
	}
	return nil
}
