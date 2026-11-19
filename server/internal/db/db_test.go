package db

import (
	"database/sql"
	"testing"
)

func tableExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("query sqlite_master for %q: %v", name, err)
	}
	return n > 0
}

// TestOpenCreatesCurrentSchema pins the schema to the current design: the
// retired password tables must not come back, and every table the server
// actually uses must exist after a fresh Open.
func TestOpenCreatesCurrentSchema(t *testing.T) {
	conn, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	for _, gone := range []string{"admin_users", "sessions"} {
		if tableExists(t, conn, gone) {
			t.Errorf("retired table %q still present in a fresh database", gone)
		}
	}
	for _, want := range []string{"inbox_items", "blobs", "pairing_codes", "paired_devices"} {
		if !tableExists(t, conn, want) {
			t.Errorf("table %q missing from a fresh database", want)
		}
	}

	v, err := MigratedVersion(conn)
	if err != nil {
		t.Fatalf("migrated version: %v", err)
	}
	if v != 2 {
		t.Fatalf("MigratedVersion = %d, want 2", v)
	}
}
