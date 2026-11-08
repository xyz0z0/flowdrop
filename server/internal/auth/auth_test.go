package auth

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Exec(`
		CREATE TABLE admin_users (
			id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
		CREATE TABLE sessions (
			id TEXT PRIMARY KEY, user_id TEXT NOT NULL,
			created_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL, user_agent TEXT NOT NULL DEFAULT '');`)
	if err != nil {
		t.Fatalf("schema: %v", err)
	}
	return conn
}

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected PHC encoding: %s", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword(hash, "correct horse battery stapl") {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("garbage", "x") {
		t.Fatal("malformed hash accepted")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a, _ := HashPassword("same password")
	b, _ := HashPassword("same password")
	if a == b {
		t.Fatal("two hashes of the same password are identical; salt is missing")
	}
}

func TestPasswordLengthBeatsTimingShortcut(t *testing.T) {
	// Empty passwords are rejected up front rather than hashed.
	if _, err := HashPassword(""); err == nil {
		t.Fatal("empty password accepted")
	}
}

func TestAuthenticateUnknownUserAndWrongPassword(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)

	if _, err := store.Authenticate("admin", "x"); err != ErrNoUser {
		t.Fatalf("err = %v, want ErrNoUser", err)
	}
	if err := store.SetPassword("admin", "a-long-enough-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Authenticate("admin", "wrong"); err != ErrInvalidPassword {
		t.Fatalf("err = %v, want ErrInvalidPassword", err)
	}
	if _, err := store.Authenticate("nobody", "a-long-enough-password"); err != ErrNoUser {
		t.Fatalf("err = %v, want ErrNoUser for unknown user", err)
	}
	if _, err := store.Authenticate("admin", "a-long-enough-password"); err != nil {
		t.Fatalf("valid login failed: %v", err)
	}
}

func TestSetPasswordIsIdempotentAndRotates(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)
	if err := store.SetPassword("admin", "first-password"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPassword("admin", "second-password"); err != nil {
		t.Fatal(err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n)
	if n != 1 {
		t.Fatalf("admin_users rows = %d, want 1", n)
	}
	if _, err := store.Authenticate("admin", "first-password"); err == nil {
		t.Fatal("old password still works after rotation")
	}
	if _, err := store.Authenticate("admin", "second-password"); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestSessionsStoreOnlyHashes(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)
	if err := store.SetPassword("admin", "a-long-enough-password"); err != nil {
		t.Fatal(err)
	}
	userID, err := store.Authenticate("admin", "a-long-enough-password")
	if err != nil {
		t.Fatal(err)
	}

	token, sess, err := store.CreateSession(userID, "test-agent")
	if err != nil {
		t.Fatal(err)
	}
	var storedID string
	if err := conn.QueryRow(`SELECT id FROM sessions`).Scan(&storedID); err != nil {
		t.Fatal(err)
	}
	if storedID == token {
		t.Fatal("plaintext session token stored")
	}
	if storedID != TokenID(token) || sess.ID != TokenID(token) {
		t.Fatal("session id is not sha256(token)")
	}

	got, err := store.LookupSession(token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.UserID != userID {
		t.Fatalf("user = %q", got.UserID)
	}

	if err := store.DeleteSession(token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupSession(token); err == nil {
		t.Fatal("deleted session still valid")
	}
}

func TestExpiredSessionIsRejectedAndPurged(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)
	store.SetPassword("admin", "a-long-enough-password")
	userID, _ := store.Authenticate("admin", "a-long-enough-password")
	token, _, err := store.CreateSession(userID, "ua")
	if err != nil {
		t.Fatal(err)
	}
	// Backdate the expiry.
	conn.Exec(`UPDATE sessions SET expires_at = ? WHERE id = ?`,
		time.Now().Add(-time.Minute).UnixMilli(), TokenID(token))

	if _, err := store.LookupSession(token); err != ErrSessionExpired {
		t.Fatalf("err = %v, want ErrSessionExpired", err)
	}
	var n int
	conn.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	if n != 0 {
		t.Fatalf("expired session row not purged: %d", n)
	}
}

func TestCleanupExpired(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)
	store.SetPassword("admin", "a-long-enough-password")
	userID, _ := store.Authenticate("admin", "a-long-enough-password")
	_, _, _ = store.CreateSession(userID, "a")
	_, _, _ = store.CreateSession(userID, "b")
	conn.Exec(`UPDATE sessions SET expires_at = ?`, time.Now().Add(-time.Hour).UnixMilli())
	n, err := store.CleanupExpired()
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("removed %d sessions, want 2", n)
	}
}

func TestSlidingExpiryExtendsIdleSession(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)
	store.SetPassword("admin", "a-long-enough-password")
	userID, _ := store.Authenticate("admin", "a-long-enough-password")
	token, _, err := store.CreateSession(userID, "ua")
	if err != nil {
		t.Fatal(err)
	}
	// Session idle for 2 hours but not yet expired.
	past := time.Now().Add(-2 * time.Hour)
	conn.Exec(`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
		past.UnixMilli(), time.Now().Add(30*time.Minute).UnixMilli(), TokenID(token))

	got, err := store.LookupSession(token)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(got.ExpiresAt) < 50*time.Minute {
		t.Fatalf("expiry was not extended: %v", got.ExpiresAt)
	}
}

func TestCreateAdminIfAbsentNeverOverwrites(t *testing.T) {
	conn := openTestDB(t)
	store := NewStore(conn, time.Hour)

	created, err := store.CreateAdminIfAbsent("admin", "first-long-password")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created {
		t.Fatal("first CreateAdminIfAbsent reported no insert")
	}

	created, err = store.CreateAdminIfAbsent("other", "second-long-password")
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	if created {
		t.Fatal("CreateAdminIfAbsent overwrote/added a second admin")
	}
	if _, err := store.Authenticate("admin", "first-long-password"); err != nil {
		t.Fatalf("original password no longer valid: %v", err)
	}
	if _, err := store.Authenticate("other", "second-long-password"); err == nil {
		t.Fatal("second admin was created")
	}
}
