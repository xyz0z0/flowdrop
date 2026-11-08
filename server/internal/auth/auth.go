// Package auth implements password hashing (Argon2id) and server-side
// sessions. Only a SHA-256 hash of each session token is persisted.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

var (
	ErrInvalidPassword = errors.New("invalid password")
	ErrNoUser          = errors.New("no admin user configured")
	ErrSessionExpired  = errors.New("session expired")
)

// Argon2id parameters (OWASP baseline for interactive login).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// HashPassword returns a PHC-encoded Argon2id hash.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", errors.New("password must not be empty")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a PHC-encoded Argon2id hash using
// constant-time comparison.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v=")); err != nil {
		return false
	}
	var m uint32
	var t uint32
	var p uint8
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return false
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return false
		}
		switch k {
		case "m":
			m = uint32(n)
		case "t":
			t = uint32(n)
		case "p":
			if n > 255 {
				return false
			}
			p = uint8(n)
		}
	}
	if m == 0 || t == 0 || p == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Session is a persisted login session.
type Session struct {
	ID         string
	UserID     string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
	UserAgent  string
}

// Store provides admin-user and session persistence.
type Store struct {
	db  *sql.DB
	ttl time.Duration
}

func NewStore(db *sql.DB, ttl time.Duration) *Store {
	return &Store{db: db, ttl: ttl}
}

// HasAdmin reports whether an admin user exists.
func (s *Store) HasAdmin() (bool, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// SetPassword creates or updates the single admin user.
func (s *Store) SetPassword(username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	var id string
	err = s.db.QueryRow(`SELECT id FROM admin_users WHERE username = ?`, username).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		_, err = s.db.Exec(`INSERT INTO admin_users(id, username, password_hash, created_at, updated_at)
			VALUES(?,?,?,?,?)`, hex.EncodeToString(buf), username, hash, now, now)
		return err
	case err != nil:
		return err
	default:
		_, err = s.db.Exec(`UPDATE admin_users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, now, id)
		return err
	}
}

// CreateAdminIfAbsent inserts the first admin user and reports whether it did.
// The INSERT only fires while admin_users is empty, so a race between the CLI
// (`flowdrop admin init`) and an embedded desktop bridge can never produce two
// administrators or overwrite an existing password hash.
func (s *Store) CreateAdminIfAbsent(username, password string) (bool, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return false, err
	}
	now := time.Now().UnixMilli()
	res, err := s.db.Exec(`INSERT INTO admin_users(id, username, password_hash, created_at, updated_at)
		SELECT ?, ?, ?, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM admin_users)`,
		hex.EncodeToString(buf), username, hash, now, now)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Authenticate verifies the admin password. It performs a dummy verify when no
// user exists so timing does not distinguish the two cases.
func (s *Store) Authenticate(username, password string) (string, error) {
	var id, hash string
	err := s.db.QueryRow(`SELECT id, password_hash FROM admin_users WHERE username = ?`, username).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		dummy, _ := HashPassword("flowdrop-dummy-password")
		VerifyPassword(dummy, password)
		return "", ErrNoUser
	}
	if err != nil {
		return "", err
	}
	if !VerifyPassword(hash, password) {
		return "", ErrInvalidPassword
	}
	return id, nil
}

// CreateSession issues a new session token. The returned token is the only
// time the plaintext token exists server-side.
func (s *Store) CreateSession(userID, userAgent string) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	sess := Session{
		ID:         TokenID(token),
		UserID:     userID,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.ttl),
		UserAgent:  userAgent,
	}
	_, err := s.db.Exec(`INSERT INTO sessions(id, user_id, created_at, last_seen_at, expires_at, user_agent)
		VALUES(?,?,?,?,?,?)`,
		sess.ID, sess.UserID, sess.CreatedAt.UnixMilli(), sess.LastSeenAt.UnixMilli(),
		sess.ExpiresAt.UnixMilli(), sess.UserAgent)
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

// LookupSession resolves a plaintext token to a session, extending its sliding
// expiry when it has been idle for more than an hour.
func (s *Store) LookupSession(token string) (Session, error) {
	var sess Session
	var created, lastSeen, expires int64
	err := s.db.QueryRow(`SELECT id, user_id, created_at, last_seen_at, expires_at, user_agent
		FROM sessions WHERE id = ?`, TokenID(token)).
		Scan(&sess.ID, &sess.UserID, &created, &lastSeen, &expires, &sess.UserAgent)
	if err != nil {
		return Session{}, err
	}
	sess.CreatedAt = time.UnixMilli(created)
	sess.LastSeenAt = time.UnixMilli(lastSeen)
	sess.ExpiresAt = time.UnixMilli(expires)
	now := time.Now()
	if now.After(sess.ExpiresAt) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE id = ?`, sess.ID)
		return Session{}, ErrSessionExpired
	}
	if now.Sub(sess.LastSeenAt) > time.Hour {
		newExpiry := now.Add(s.ttl)
		if _, err := s.db.Exec(`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`,
			now.UnixMilli(), newExpiry.UnixMilli(), sess.ID); err == nil {
			sess.LastSeenAt = now
			sess.ExpiresAt = newExpiry
		}
	}
	return sess, nil
}

// DeleteSession revokes one session (logout).
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, TokenID(token))
	return err
}

// CleanupExpired removes expired sessions; returns the number deleted.
func (s *Store) CleanupExpired() (int64, error) {
	res, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// TokenID returns the storage key for a session token.
func TokenID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewRandomToken returns a URL-safe random token of n bytes entropy.
func NewRandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
