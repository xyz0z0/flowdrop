// Package webui embeds the built frontend. The dist directory is produced by
// `npm run build` in web/; a placeholder keeps `go build` working before that.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// FS returns the frontend asset filesystem, or nil when the build has not run
// (only the placeholder file is present).
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
