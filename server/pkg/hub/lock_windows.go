//go:build windows

package hub

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/windows"
)

// tryLockFile takes a non-blocking exclusive lock with LockFileEx. Windows
// releases it when the handle is closed, including on abnormal process exit.
func tryLockFile(f *os.File) (any, error) {
	ov := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, ov,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrDataDirInUse
		}
		return nil, err
	}
	return ov, nil
}

func unlockFile(f *os.File, token any) error {
	ov, ok := token.(*windows.Overlapped)
	if !ok || ov == nil {
		return nil
	}
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, ov)
}

func pidString() string { return strconv.Itoa(os.Getpid()) }

// isAddrInUse reports the "port already taken" bind failure.
func isAddrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
