// Package storage manages the on-disk layout: blobs by random key, a tmp dir
// for in-flight uploads, and cleanup of temp/orphaned files.
package storage

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Store owns the data directory layout.
type Store struct {
	root string
	// active tracks temp files currently being written so the janitor never
	// removes an in-flight upload.
	mu     sync.Mutex
	active map[string]struct{}
}

func New(root string) (*Store, error) {
	s := &Store{root: root, active: make(map[string]struct{})}
	for _, d := range []string{root, filepath.Join(root, "blobs"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	return s, nil
}

func (s *Store) Root() string { return s.root }

// NewTempFile creates a random temp file and registers it as active.
func (s *Store) NewTempFile() (*os.File, string, error) {
	key, err := randomKey()
	if err != nil {
		return nil, "", err
	}
	name := filepath.Join(s.root, "tmp", key+".part")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, "", err
	}
	s.mu.Lock()
	s.active[name] = struct{}{}
	s.mu.Unlock()
	return f, name, nil
}

// ReleaseTemp clears the active marker for a temp path.
func (s *Store) ReleaseTemp(path string) {
	s.mu.Lock()
	delete(s.active, path)
	s.mu.Unlock()
}

// NewBlobKey returns a fresh random blob key.
func (s *Store) NewBlobKey() (string, error) { return randomKey() }

// BlobPath maps a storage key to its path. Keys are server-generated hex, but
// this still validates to defend against corrupted metadata.
func (s *Store) BlobPath(key string) (string, error) {
	if !validKey(key) {
		return "", errors.New("invalid storage key")
	}
	return filepath.Join(s.root, "blobs", key[:2], key), nil
}

// CommitTemp atomically moves a fully-written temp file into blob storage and
// fsyncs the destination directory.
func (s *Store) CommitTemp(tempPath, key string) error {
	dst, err := s.BlobPath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	if err := os.Rename(tempPath, dst); err != nil {
		return err
	}
	s.ReleaseTemp(tempPath)
	return syncDir(filepath.Dir(dst))
}

// OpenBlob opens a stored blob for reading.
func (s *Store) OpenBlob(key string) (*os.File, error) {
	p, err := s.BlobPath(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// RemoveBlob deletes a stored blob; missing files are not an error.
func (s *Store) RemoveBlob(key string) error {
	p, err := s.BlobPath(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveTemp deletes a temp file; missing files are not an error.
func (s *Store) RemoveTemp(path string) error {
	s.ReleaseTemp(path)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// AvailableBytes reports free bytes on the filesystem holding the data dir.
func (s *Store) AvailableBytes() (uint64, error) {
	return availableBytes(s.root)
}

// CleanupTemp removes temp files older than maxAge that are not active.
func (s *Store) CleanupTemp(maxAge time.Duration) (int, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "tmp"))
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		p := filepath.Join(s.root, "tmp", e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		s.mu.Lock()
		_, busy := s.active[p]
		s.mu.Unlock()
		if busy {
			continue
		}
		if err := os.Remove(p); err == nil {
			n++
		}
	}
	return n, nil
}

// RemoveUnknownBlobs deletes blob files on disk whose key is not in known and
// whose mtime is older than cutoff. Returns the number of files removed.
func (s *Store) RemoveUnknownBlobs(known map[string]struct{}, cutoff time.Time) (int, error) {
	root := filepath.Join(s.root, "blobs")
	shards, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, shard.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			if _, ok := known[f.Name()]; ok {
				continue
			}
			info, err := f.Info()
			if err != nil || info.ModTime().After(cutoff) {
				continue
			}
			if err := os.Remove(filepath.Join(root, shard.Name(), f.Name())); err == nil {
				removed++
			}
		}
	}
	return removed, nil
}

// WriteReader copies a reader into a blob (used by tests/imports). The caller
// is responsible for size limits.
func (s *Store) WriteReader(r io.Reader, key string) (int64, error) {
	p, err := s.BlobPath(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		return n, err
	}
	return n, f.Sync()
}

func randomKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validKey(key string) bool {
	if len(key) != 32 {
		return false
	}
	for _, r := range key {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// syncDir is defined per platform in sync_unix.go (real fsync) and
// sync_windows.go (directory fsync is not supported by Windows).
