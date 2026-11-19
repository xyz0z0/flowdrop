//go:build linux && cgo && gtk3

package main

import "testing"

// The helper only talks to WebKit's default WebContext/WebsiteDataManager, so
// it must not require a window, a running GTK main loop or a display. The real
// HTTP+WebSocket behaviour is covered by TestWebViewProxyE2E under the
// flowdrop_webview_proxy_probe tag.
func TestConfigureDesktopWebViewProxy(t *testing.T) {
	if err := configureDesktopWebViewProxy(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := configureDesktopWebViewProxy(); err != nil {
		t.Fatalf("second (idempotent) call: %v", err)
	}
}
