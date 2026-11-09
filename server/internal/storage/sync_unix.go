//go:build unix

package storage

import "os"

// syncDir fsyncs a directory so that a rename into it is durable.
// Unix allows fsync on a directory handle, so this is a real barrier.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
