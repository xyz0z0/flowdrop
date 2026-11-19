// Package db opens the SQLite database and applies versioned migrations.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open opens (creating if needed) the SQLite database inside dataDir.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	path := filepath.Join(dataDir, "flowdrop.db")
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	q.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + path + "?" + q.Encode()

	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	conn.SetMaxOpenConns(1) // SQLite writer serialization; keeps behavior predictable.
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

var migrations = []string{
	// v1
	`
CREATE TABLE blobs (
  id            TEXT PRIMARY KEY,
  original_name TEXT NOT NULL,
  content_type  TEXT NOT NULL,
  size_bytes    INTEGER NOT NULL,
  sha256        TEXT NOT NULL,
  storage_key   TEXT NOT NULL UNIQUE,
  created_at    INTEGER NOT NULL
);

CREATE TABLE inbox_items (
  id            TEXT PRIMARY KEY,
  type          TEXT NOT NULL CHECK (type IN ('text','link','image','file')),
  text_content  TEXT,
  blob_id       TEXT REFERENCES blobs(id),
  sender_device TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  expires_at    INTEGER
);
CREATE INDEX idx_inbox_items_order ON inbox_items(created_at DESC, id DESC);
CREATE INDEX idx_inbox_items_blob ON inbox_items(blob_id);
`,
	// v2: device pairing (see internal/pairing and docs/PAIRING.md).
	`
CREATE TABLE pairing_meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE pairing_state (
  id              INTEGER PRIMARY KEY CHECK (id = 1),
  failed_attempts INTEGER NOT NULL DEFAULT 0
);
INSERT INTO pairing_state(id, failed_attempts) VALUES (1, 0);

CREATE TABLE pairing_codes (
  code_hash  TEXT PRIMARY KEY,            -- sha256(pairing code) hex; raw code is never stored
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER
);

CREATE TABLE paired_devices (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  token_hash   TEXT NOT NULL UNIQUE,      -- sha256(device token) hex; raw token is never stored
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  revoked_at   INTEGER
);
CREATE INDEX idx_paired_devices_created ON paired_devices(created_at, id);
`,
}

func migrate(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	var current int
	if err := conn.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	for i := current; i < len(migrations); i++ {
		v := i + 1
		tx, err := conn.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", v, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, strftime('%s','now')*1000)`, v); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", v, err)
		}
	}
	return nil
}

// MigratedVersion reports the current schema version (for doctor output).
func MigratedVersion(conn *sql.DB) (int, error) {
	var v int
	err := conn.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&v)
	return v, err
}
