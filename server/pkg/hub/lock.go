package hub

import (
	"os"
	"sync"
)

// dataDirLock is an advisory, non-blocking exclusive lock on
// <data_dir>/hub.lock. It keeps the CLI and an embedded desktop Hub from
// silently writing the same SQLite database and attachment tree at once.
//
// The lock is released by Close and, on platforms with kernel file locks, by
// the OS when the process exits (including a crash), so a stale lock never
// blocks a restart forever.
type dataDirLock struct {
	f     *os.File
	token any // platform lock state (OVERLAPPED on Windows, nil elsewhere)

	once sync.Once
	err  error
}

// lockDataDir creates/opens path and takes the exclusive lock. It returns
// ErrDataDirInUse when another live process already holds it.
func lockDataDir(path string) (*dataDirLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	token, err := tryLockFile(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	// Record the owner for humans inspecting the file; the lock itself, not
	// this content, is authoritative.
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte("pid="+pidString()+"\n"), 0)
	}
	return &dataDirLock{f: f, token: token}, nil
}

// Close releases the lock and closes the file. It is idempotent.
func (l *dataDirLock) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.f == nil {
			return
		}
		l.err = unlockFile(l.f, l.token)
		if cerr := l.f.Close(); l.err == nil {
			l.err = cerr
		}
		l.f = nil
	})
	return l.err
}
