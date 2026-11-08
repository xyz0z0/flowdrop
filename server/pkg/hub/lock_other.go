//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || android || windows)

package hub

import (
	"os"
	"strconv"
)

// This fallback keeps exotic targets buildable (the rest of the server already
// only builds on the platforms above). The single-writer guard is best-effort
// here: the lock is not enforced, but the file is still created so operators
// can see the owner PID.
func tryLockFile(*os.File) (any, error) { return nil, nil }

func unlockFile(*os.File, any) error { return nil }

func pidString() string { return strconv.Itoa(os.Getpid()) }

// isAddrInUse is best-effort on unsupported targets.
func isAddrInUse(error) bool { return false }
