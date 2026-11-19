//go:build windows

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// installInjectedJS runs the desktop's page scripts after every completed
// navigation, because Wails v3.1.0 only honours WebviewWindowOptions.JS for
// windows created from options.HTML on Windows
// (application/webview_window_windows.go: navigateInitialPage passes it to
// chromium.Init only in the options.HTML branch). Both FlowDrop windows are
// created from a URL instead, so on Windows the injected title bar, the 设置
// entry, the file-drop bridge, link interception and the passwordless login
// bootstrap would never reach the page.
//
// The callback does not run on the WebView2/UI thread: Wails posts the event on
// a channel and dispatches it from a goroutine of its own. ExecJS uses
// InvokeSync once the runtime is ready (it queues into pendingJS before that),
// so it must not run on that shared dispatcher goroutine — one slow injection
// would hold up every other window event. Hence the short hop to a goroutine of
// its own; for the same reason it cannot wait on itself.
func installInjectedJS(w *application.WebviewWindow, js string) {
	if w == nil || js == "" {
		return
	}
	w.OnWindowEvent(events.Windows.WebViewNavigationCompleted, func(*application.WindowEvent) {
		go w.ExecJS(js)
	})
}
