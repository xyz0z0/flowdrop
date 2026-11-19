//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || android

package hub

import (
	"errors"
	"os"
	"strconv"
	"syscall"
)

// tryLockFile takes a non-blocking exclusive flock(2) on f. The kernel drops
// the lock automatically when the file descriptor is closed, including on
// abnormal process exit.
func tryLockFile(f *os.File) (any, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrDataDirInUse
		}
		return nil, err
	}
	return nil, nil
}

func unlockFile(f *os.File, _ any) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

func pidString() string { return strconv.Itoa(os.Getpid()) }

// isAddrInUse reports the "port already taken" bind failure.
func isAddrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
