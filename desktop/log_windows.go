//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
)

// openLogFile returns an append-only log file next to the desktop's data,
// so a GUI build (no console) still leaves a diagnosable trail.
func openLogFile() (io.WriteCloser, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "flowdrop")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(
		filepath.Join(dir, "desktop.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0o644,
	)
}
