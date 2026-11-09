//go:build windows

package main

// configureDesktopWebViewProxy reports that this build's in-app WebView
// already ignores the host's HTTP proxy.
//
// Why there is no runtime work here: WebView2 shares a single browser
// environment per process, and that environment must be created before any
// window exists. The direct connection is therefore configured statically in
// application.Options.Windows.AdditionalBrowserArgs as "--no-proxy-server"
// (see main.go), so by the time this function runs the bypass is already in
// effect for both the Hub window and the settings window.
//
// Returning nil keeps the caller on the "configured" path: it records that the
// bypass is active instead of logging "proxy bypass not configured" and showing
// a "not configured" diagnostic in the settings window, both of which would be
// wrong on Windows.
func configureDesktopWebViewProxy() error { return nil }
