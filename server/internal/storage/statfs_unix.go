//go:build unix

package storage

import "syscall"

// availableBytes reports the bytes free on the filesystem holding dir, as
// seen by a non-privileged user (Bavail, not Bfree).
func availableBytes(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}
