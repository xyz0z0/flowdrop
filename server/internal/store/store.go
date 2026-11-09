// Package store provides data access for inbox items, blobs and cleanup
// queries. SQL is hand-written; SQLite is the authoritative state.
package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"flowdrop/internal/token"
)

var ErrNotFound = errors.New("not found")

type Blob struct {
	ID           string `json:"id"`
	OriginalName string `json:"name"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	StorageKey   string `json:"-"`
	CreatedAt    int64  `json:"created_at"`
}

// IsPreviewableImage reports whether the blob may be rendered inline.
func (b *Blob) IsPreviewableImage() bool {
	if b == nil {
		return false
	}
	switch b.ContentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	}
	return false
}

type Item struct {
	ID           string `json:"id"`
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	SenderDevice string `json:"sender_device"`
	CreatedAt    int64  `json:"created_at"`
	Blob         *Blob  `json:"blob,omitempty"`
}

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

func NewID() string {
	// 16 random bytes, URL-safe, no client-controlled input.
	return token.Random(16)
}

const itemSelect = `
SELECT i.id, i.type, COALESCE(i.text_content,''), i.sender_device, i.created_at,
       b.id, b.original_name, b.content_type, b.size_bytes, b.sha256, b.storage_key, b.created_at
FROM inbox_items i
LEFT JOIN blobs b ON b.id = i.blob_id
`

func scanItem(s interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var bID, bName, bType, bSHA, bKey sql.NullString
	var bSize, bCreated sql.NullInt64
	if err := s.Scan(&it.ID, &it.Type, &it.Text, &it.SenderDevice, &it.CreatedAt,
		&bID, &bName, &bType, &bSize, &bSHA, &bKey, &bCreated); err != nil {
		return Item{}, err
	}
	if bID.Valid {
		it.Blob = &Blob{
			ID:           bID.String,
			OriginalName: bName.String,
			ContentType:  bType.String,
			SizeBytes:    bSize.Int64,
			SHA256:       bSHA.String,
			StorageKey:   bKey.String,
			CreatedAt:    bCreated.Int64,
		}
	}
	return it, nil
}

// CreateTextItem inserts a text or link item.
func (s *Store) CreateTextItem(id, itemType, text, senderDevice string) (Item, error) {
	now := time.Now().UnixMilli()
	_, err := s.db.Exec(`INSERT INTO inbox_items(id, type, text_content, sender_device, created_at)
		VALUES(?,?,?,?,?)`, id, itemType, text, senderDevice, now)
	if err != nil {
		return Item{}, err
	}
	return Item{ID: id, Type: itemType, Text: text, SenderDevice: senderDevice, CreatedAt: now}, nil
}

// CreateBlobItem inserts a blob row and its item in one transaction.
func (s *Store) CreateBlobItem(blob Blob, itemType, senderDevice string) (Item, error) {
	itemID := NewID()
	now := time.Now().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO blobs(id, original_name, content_type, size_bytes, sha256, storage_key, created_at)
		VALUES(?,?,?,?,?,?,?)`,
		blob.ID, blob.OriginalName, blob.ContentType, blob.SizeBytes, blob.SHA256, blob.StorageKey, now); err != nil {
		return Item{}, err
	}
	if _, err := tx.Exec(`INSERT INTO inbox_items(id, type, blob_id, sender_device, created_at)
		VALUES(?,?,?,?,?)`, itemID, itemType, blob.ID, senderDevice, now); err != nil {
		return Item{}, err
	}
	if err := tx.Commit(); err != nil {
		return Item{}, err
	}
	blob.CreatedAt = now
	return Item{ID: itemID, Type: itemType, SenderDevice: senderDevice, CreatedAt: now, Blob: &blob}, nil
}

// GetItem returns one item or ErrNotFound.
func (s *Store) GetItem(id string) (Item, error) {
	it, err := scanItem(s.db.QueryRow(itemSelect+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return it, err
}

// Cursor encodes the keyset position for descending pagination.
type Cursor struct {
	CreatedAt int64
	ID        string
}

func EncodeCursor(it Item) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", it.CreatedAt, it.ID)))
}

func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return Cursor{}, errors.New("invalid cursor")
	}
	var at int64
	if _, err := fmt.Sscanf(ts, "%d", &at); err != nil {
		return Cursor{}, errors.New("invalid cursor")
	}
	if id == "" {
		return Cursor{}, errors.New("invalid cursor")
	}
	return Cursor{CreatedAt: at, ID: id}, nil
}

// ListItems returns items strictly older than cursor (created_at DESC, id DESC),
// plus the cursor for the next page if more rows exist.
func (s *Store) ListItems(cursor *Cursor, limit int) ([]Item, *Cursor, error) {
	var rows *sql.Rows
	var err error
	if cursor == nil {
		rows, err = s.db.Query(itemSelect+` ORDER BY i.created_at DESC, i.id DESC LIMIT ?`, limit+1)
	} else {
		rows, err = s.db.Query(itemSelect+`
			WHERE (i.created_at < ?) OR (i.created_at = ? AND i.id < ?)
			ORDER BY i.created_at DESC, i.id DESC LIMIT ?`,
			cursor.CreatedAt, cursor.CreatedAt, cursor.ID, limit+1)
	}
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	items := make([]Item, 0, limit)
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	var next *Cursor
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = &Cursor{CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return items, next, nil
}

// DeleteItem removes an item and, when its blob is no longer referenced,
// deletes the blob row too. Returns the storage key that should now be
// unlinked from disk (empty if none). The item row is removed first so it is
// invisible even if the file unlink later fails; the janitor retries.
func (s *Store) DeleteItem(id string) (blobKey string, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var blobID sql.NullString
	err = tx.QueryRow(`SELECT blob_id FROM inbox_items WHERE id = ?`, id).Scan(&blobID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`DELETE FROM inbox_items WHERE id = ?`, id); err != nil {
		return "", err
	}
	if blobID.Valid {
		var refs int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM inbox_items WHERE blob_id = ?`, blobID.String).Scan(&refs); err != nil {
			return "", err
		}
		if refs == 0 {
			if err := tx.QueryRow(`SELECT storage_key FROM blobs WHERE id = ?`, blobID.String).Scan(&blobKey); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", err
			}
			if _, err := tx.Exec(`DELETE FROM blobs WHERE id = ?`, blobID.String); err != nil {
				return "", err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return blobKey, nil
}

// OrphanBlobs lists blobs with no referencing item that are older than cutoff.
func (s *Store) OrphanBlobs(cutoff time.Time) ([]Blob, error) {
	rows, err := s.db.Query(`SELECT b.id, b.original_name, b.content_type, b.size_bytes, b.sha256, b.storage_key, b.created_at
		FROM blobs b
		LEFT JOIN inbox_items i ON i.blob_id = b.id
		WHERE i.id IS NULL AND b.created_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Blob
	for rows.Next() {
		var b Blob
		if err := rows.Scan(&b.ID, &b.OriginalName, &b.ContentType, &b.SizeBytes, &b.SHA256, &b.StorageKey, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBlobRow removes a blob row by ID.
func (s *Store) DeleteBlobRow(id string) error {
	_, err := s.db.Exec(`DELETE FROM blobs WHERE id = ?`, id)
	return err
}

// KnownBlobKeys returns every storage key currently recorded in the DB.
func (s *Store) KnownBlobKeys() (map[string]struct{}, error) {
	rows, err := s.db.Query(`SELECT storage_key FROM blobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make(map[string]struct{})
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys[k] = struct{}{}
	}
	return keys, rows.Err()
}

// Stats returns item and blob counts (for doctor).
func (s *Store) Stats() (items, blobs int64, err error) {
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&items); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&blobs)
	return
}
