package storage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNewCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"blobs", "tmp"} {
		info, err := os.Stat(filepath.Join(dir, sub))
		if err != nil || !info.IsDir() {
			t.Fatalf("%s missing: %v", sub, err)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("%s perm = %o, want 700", sub, perm)
		}
	}
}

func TestCommitTempIsAtomicMove(t *testing.T) {
	s := newStore(t)
	f, tmpPath, err := s.NewTempFile()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("payload"); err != nil {
		t.Fatal(err)
	}
	f.Sync()
	f.Close()

	key, _ := s.NewBlobKey()
	if err := s.CommitTemp(tmpPath, key); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatal("temp file still present after commit")
	}
	blobPath, err := s.BlobPath(key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(blobPath)
	if err != nil {
		t.Fatalf("blob missing: %v", err)
	}
	if string(data) != "payload" {
		t.Fatalf("blob content = %q", data)
	}
}

func TestBlobPathRejectsBadKeys(t *testing.T) {
	s := newStore(t)
	for _, key := range []string{
		"", "short", strings.Repeat("a", 33), "../../etc/passwd",
		strings.Repeat("Z", 32), strings.Repeat("a", 31) + "/",
	} {
		if _, err := s.BlobPath(key); err == nil {
			t.Errorf("BlobPath accepted invalid key %q", key)
		}
	}
}

func TestCleanupTempSkipsActiveUploads(t *testing.T) {
	s := newStore(t)
	old := time.Now().Add(-2 * time.Hour)

	stale := filepath.Join(s.Root(), "tmp", "stale.part")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(stale, old, old)

	_, activePath, err := s.NewTempFile()
	if err != nil {
		t.Fatal(err)
	}
	os.Chtimes(activePath, old, old) // old mtime, but still registered

	n, err := s.CleanupTemp(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed %d temp files, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("stale temp file survived")
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Error("active upload temp file was deleted")
	}
	s.RemoveTemp(activePath)
}

func TestRemoveUnknownBlobsRespectsGrace(t *testing.T) {
	s := newStore(t)
	knownKey, _ := s.NewBlobKey()
	unknownKey, _ := s.NewBlobKey()
	recentKey, _ := s.NewBlobKey()
	for _, k := range []string{knownKey, unknownKey, recentKey} {
		if _, err := s.WriteReader(strings.NewReader("x"), k); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	for _, k := range []string{knownKey, unknownKey} {
		p, _ := s.BlobPath(k)
		os.Chtimes(p, old, old)
	}

	known := map[string]struct{}{knownKey: {}}
	n, err := s.RemoveUnknownBlobs(known, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if p, _ := s.BlobPath(knownKey); !fileExists(p) {
		t.Error("known blob was removed")
	}
	if p, _ := s.BlobPath(unknownKey); fileExists(p) {
		t.Error("stale unknown blob survived")
	}
	if p, _ := s.BlobPath(recentKey); !fileExists(p) {
		t.Error("recent unknown blob was removed before the grace period")
	}
}

func TestWriteReaderAndOpenBlob(t *testing.T) {
	s := newStore(t)
	key, _ := s.NewBlobKey()
	n, err := s.WriteReader(bytes.NewReader([]byte("hello")), key)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("wrote %d bytes", n)
	}
	f, err := s.OpenBlob(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	if string(data) != "hello" {
		t.Fatalf("read %q", data)
	}
	// Second write to the same key must fail rather than clobber.
	if _, err := s.WriteReader(strings.NewReader("again"), key); err == nil {
		t.Fatal("WriteReader overwrote an existing blob")
	}
}

func TestRemoveMissingIsNotAnError(t *testing.T) {
	s := newStore(t)
	key, _ := s.NewBlobKey()
	if err := s.RemoveBlob(key); err != nil {
		t.Fatalf("RemoveBlob on missing file: %v", err)
	}
	if err := s.RemoveTemp(filepath.Join(s.Root(), "tmp", "nope.part")); err != nil {
		t.Fatalf("RemoveTemp on missing file: %v", err)
	}
}

func TestAvailableBytes(t *testing.T) {
	s := newStore(t)
	n, err := s.AvailableBytes()
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("reported zero free bytes")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
