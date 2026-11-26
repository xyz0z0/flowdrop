//go:build !linux || !cgo || !gtk3

package main

import "log"

// installDownloadHandler is the stub for builds without the Linux GTK3/WebKitGTK
// backend. There is no WebKitWebContext to hook there: the Windows/macOS
// backends hand downloads to their own browser stack, which already saves into
// the user's download directory under a free name, and the page keeps its
// "已发起下载" fallback (no native outcome is reported). The desktop logs that no
// in-process destination handler exists so a same-name problem on another
// platform is not mistaken for this code being active.
func installDownloadHandler() {
	log.Printf("downloads: in-app download destination handler requires a linux + cgo + gtk3 build; backend default kept")
}

// downloadStatusBridgeEnabled is false here: the page must not switch to the
// native download state machine when no WebKit context reports outcomes.
const downloadStatusBridgeEnabled = false
