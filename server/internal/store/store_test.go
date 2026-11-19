package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"flowdrop/internal/db"
)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return New(conn), conn
}

func TestCursorRoundTrip(t *testing.T) {
	it := Item{ID: "abc123", CreatedAt: 1700000000123}
	enc := EncodeCursor(it)
	cur, err := DecodeCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cur.ID != it.ID || cur.CreatedAt != it.CreatedAt {
		t.Fatalf("cursor = %+v, want %+v", cur, it)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, s := range []string{"", "!!!!", "bm90LWEtY3Vyc29y", "MTIzfA"} {
		if _, err := DecodeCursor(s); err == nil {
			t.Errorf("DecodeCursor(%q) succeeded", s)
		}
	}
}

func TestListItemsKeysetPagination(t *testing.T) {
	s, conn := newTestStore(t)
	// Insert with explicit timestamps: 10 items, some sharing a timestamp.
	base := time.Now().UnixMilli() - 100_000
	for i := 0; i < 10; i++ {
		ts := base + int64(i/2)*1000 // pairs share a timestamp
		if _, err := conn.Exec(`INSERT INTO inbox_items(id, type, text_content, sender_device, created_at)
			VALUES(?,?,?,?,?)`, string(rune('a'+i))+"id", "text", "x", "", ts); err != nil {
			t.Fatal(err)
		}
	}
	var all []string
	var cursor *Cursor
	for i := 0; i < 20; i++ {
		items, next, err := s.ListItems(cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			all = append(all, it.ID)
		}
		if next == nil {
			break
		}
		cursor = next
	}
	if len(all) != 10 {
		t.Fatalf("collected %d items: %v", len(all), all)
	}
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("duplicate %s", id)
		}
		seen[id] = true
	}
}

func TestCreateAndDeleteBlobItem(t *testing.T) {
	s, conn := newTestStore(t)
	blob := Blob{
		ID: "b1", OriginalName: "a.png", ContentType: "image/png",
		SizeBytes: 10, SHA256: "abc", StorageKey: "00112233445566778899aabbccddeeff",
	}
	it, err := s.CreateBlobItem(blob, "image", "phone")
	if err != nil {
		t.Fatal(err)
	}
	if it.Blob == nil || it.Blob.ID != "b1" {
		t.Fatalf("item blob = %+v", it.Blob)
	}

	got, err := s.GetItem(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Blob == nil || got.Blob.OriginalName != "a.png" {
		t.Fatalf("roundtrip blob = %+v", got.Blob)
	}

	key, err := s.DeleteItem(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != blob.StorageKey {
		t.Fatalf("blob key = %q", key)
	}
	if _, err := s.GetItem(it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&n)
	if n != 0 {
		t.Fatalf("blob rows = %d", n)
	}
}

func TestDeleteTextItemReturnsNoKey(t *testing.T) {
	s, _ := newTestStore(t)
	it, err := s.CreateTextItem(NewID(), "text", "hi", "d")
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.DeleteItem(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		t.Fatalf("text item returned blob key %q", key)
	}
}

func TestDeleteMissingItem(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.DeleteItem("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestOrphanBlobsIgnoresReferenced(t *testing.T) {
	s, conn := newTestStore(t)
	referenced := Blob{ID: "keep", OriginalName: "k", ContentType: "text/plain",
		SizeBytes: 1, SHA256: "x", StorageKey: "11111111111111111111111111111111"}
	if _, err := s.CreateBlobItem(referenced, "file", ""); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour).UnixMilli()
	conn.Exec(`INSERT INTO blobs(id, original_name, content_type, size_bytes, sha256, storage_key, created_at)
		VALUES('orphan','o','text/plain',1,'y','22222222222222222222222222222222',?)`, old)
	// A recent orphan must be excluded by the grace cutoff.
	conn.Exec(`INSERT INTO blobs(id, original_name, content_type, size_bytes, sha256, storage_key, created_at)
		VALUES('fresh','f','text/plain',1,'z','33333333333333333333333333333333',?)`, time.Now().UnixMilli())

	orphans, err := s.OrphanBlobs(time.Now().Add(-10 * time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) != 1 || orphans[0].ID != "orphan" {
		t.Fatalf("orphans = %+v", orphans)
	}
}

func TestKnownBlobKeys(t *testing.T) {
	s, _ := newTestStore(t)
	blob := Blob{ID: "b", OriginalName: "x", ContentType: "text/plain",
		SizeBytes: 1, SHA256: "s", StorageKey: "44444444444444444444444444444444"}
	if _, err := s.CreateBlobItem(blob, "file", ""); err != nil {
		t.Fatal(err)
	}
	keys, err := s.KnownBlobKeys()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys[blob.StorageKey]; !ok {
		t.Fatalf("keys = %v", keys)
	}
}
