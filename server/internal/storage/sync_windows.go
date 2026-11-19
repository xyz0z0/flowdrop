//go:build windows

package storage

import "os"

// syncDir is the durability barrier for a directory after a rename.
// Windows has no equivalent of fsync for a directory handle: FlushFileBuffers
// fails on handles opened without write access (which is how os.Open opens a
// directory), so this only verifies the directory is reachable and returns.
// The file content itself is already flushed by f.Sync() before the rename.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return d.Close()
}
