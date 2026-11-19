//go:build !windows && (!linux || !cgo || !gtk3)

package main

import "errors"

// configureDesktopWebViewProxy is a stub for builds that do not use the
// Linux GTK3/WebKitGTK 4.1 backend. It reports that no in-process WebView
// proxy bypass was applied, so callers keep their proxy diagnostics instead
// of silently claiming the problem is fixed.
func configureDesktopWebViewProxy() error {
	return errors.New("当前构建未启用 WebKitGTK 代理直连配置（需要 linux + cgo + gtk3）")
}
