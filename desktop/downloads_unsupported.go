//go:build !linux || !cgo || !gtk3

package main

import "log"

// installDownloadHandler is the stub for builds without the Linux GTK3/WebKitGTK
// backend. There is no WebKitWebContext to hook there: the Windows/macOS
// backends hand downloads to their own browser stack, which already saves into
// the user's download directory under a free name. The desktop logs that no
// in-process destination handler exists so a same-name problem on another
// platform is not mistaken for this code being active.
func installDownloadHandler() {
	log.Printf("downloads: in-app download destination handler requires a linux + cgo + gtk3 build; backend default kept")
}
