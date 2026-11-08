// Package pairing implements the device pairing core: short-lived 4-digit
// codes, per-device tokens and the stable hub ID.
//
// Cold facts (see docs/PAIRING.md):
//   - Codes are 4 decimal digits, may start with 0, live 120s, are single-use
//     and a newly generated code invalidates any older one.
//   - Five wrong codes lock pairing for the whole hub until a new code is
//     generated locally (CreateCode is the only unlock path).
//   - Device tokens are 32 random bytes (base64url). Only their SHA-256 hash
//     is persisted; the raw token is returned once from Pair and never logged.
//   - The hub ID is random, stable and persisted; it is not a secret.
//
// Callers pass the current time so tests and the HTTP layer stay in control.
package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrInvalidCode covers wrong, expired, already-consumed and unknown codes.
	ErrInvalidCode = errors.New("pairing code invalid or expired")
	// ErrLocked means the whole hub has exhausted its pairing attempts.
	ErrLocked = errors.New("pairing locked after too many attempts")
	// ErrUnauthorized covers unknown/revoked tokens and unknown device IDs.
	ErrUnauthorized = errors.New("unauthorized")
)

const (
	codeDigits     = 4
	codeTTL        = 120 * time.Second
	maxFailed      = 5
	maxDeviceName  = 64
	tokenBytes     = 32
	deviceIDBytes  = 16
	hubIDBytes     = 16
	hubIDKey       = "hub_id"
	pairingStateID = 1
	// maxCodeAttempts bounds the retry loop that avoids replacing a code with
	// the same 4-digit value. A random candidate collides 1 in 10000 times, so
	// in practice one attempt suffices; the bound makes failure explicit.
	maxCodeAttempts = 32
)

// errInvalidName is intentionally unexported: the frozen interface only
// exposes the three sentinels above, and callers should validate input before
// calling Pair.
var errInvalidName = errors.New("pairing: device name must not be empty")

// Code is a freshly generated pairing code. The raw Code value is only ever
// shown locally and is never persisted in clear text.
type Code struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expires_at"`
}

// Device is a paired device as listed locally. It carries no token material.
type Device struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
}

// Store is the pairing persistence layer over the shared SQLite database.
type Store struct{ db *sql.DB }

// NewStore wraps an already migrated *sql.DB.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// HubID returns the stable hub identifier, generating and persisting it on
// first use. Concurrent callers (including a separate CLI process) converge on
// a single value because the insert is atomic and the value is re-read.
func (s *Store) HubID() (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT value FROM pairing_meta WHERE key = ?`, hubIDKey).Scan(&id)
	if err == nil && id != "" {
		return id, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read hub id: %w", err)
	}
	candidate, err := randomToken(hubIDBytes)
	if err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO pairing_meta(key, value) VALUES(?, ?)`, hubIDKey, candidate); err != nil {
		return "", fmt.Errorf("persist hub id: %w", err)
	}
	if err := s.db.QueryRow(`SELECT value FROM pairing_meta WHERE key = ?`, hubIDKey).Scan(&id); err != nil {
		return "", fmt.Errorf("read hub id: %w", err)
	}
	if id == "" {
		return "", errors.New("hub id missing after persist")
	}
	return id, nil
}

// CreateCode generates a new code, dropping any previous one and resetting the
// failed-attempt counter. Generating a code is a local-only action, so it is
// also the way out of a locked state. The new code is guaranteed to differ
// from the code it replaces, so regenerating can never leave the old value
// usable (see TestNewCodeInvalidatesOld).
func (s *Store) CreateCode(now time.Time) (Code, error) {
	return s.createCode(now, randomCode)
}

// createCode is CreateCode with the candidate generator injected so tests can
// force a collision with the previous code deterministically. Callers use
// CreateCode; the generator is never exposed publicly.
func (s *Store) createCode(now time.Time, gen func() (string, error)) (Code, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Code{}, fmt.Errorf("begin create code: %w", err)
	}
	// The previous row is read inside the transaction, so the comparison uses
	// the code actually being replaced rather than a value read beforehand.
	var prevHash string
	switch err := tx.QueryRow(`SELECT code_hash FROM pairing_codes ORDER BY created_at DESC, rowid DESC LIMIT 1`).Scan(&prevHash); {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		prevHash = ""
	default:
		tx.Rollback()
		return Code{}, fmt.Errorf("read previous pairing code: %w", err)
	}
	code := ""
	found := false
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		candidate, err := gen()
		if err != nil {
			tx.Rollback()
			return Code{}, err
		}
		if hashSecret(candidate) != prevHash {
			code, found = candidate, true
			break
		}
	}
	if !found {
		tx.Rollback()
		return Code{}, fmt.Errorf("generate pairing code: %d candidates all matched the previous code", maxCodeAttempts)
	}
	nowMs := now.UnixMilli()
	expiresAt := now.Add(codeTTL).UnixMilli()
	if _, err := tx.Exec(`UPDATE pairing_state SET failed_attempts = 0 WHERE id = ?`, pairingStateID); err != nil {
		tx.Rollback()
		return Code{}, fmt.Errorf("reset pairing attempts: %w", err)
	}
	// A new code invalidates every older one, including consumed rows.
	if _, err := tx.Exec(`DELETE FROM pairing_codes`); err != nil {
		tx.Rollback()
		return Code{}, fmt.Errorf("drop old pairing codes: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO pairing_codes(code_hash, created_at, expires_at) VALUES(?, ?, ?)`,
		hashSecret(code), nowMs, expiresAt); err != nil {
		tx.Rollback()
		return Code{}, fmt.Errorf("store pairing code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Code{}, fmt.Errorf("commit pairing code: %w", err)
	}
	return Code{Code: code, ExpiresAt: expiresAt}, nil
}

// Pair consumes code and registers a device. Exactly one caller can win per
// code: consumption is a conditional UPDATE whose affected-row count decides
// the winner, so concurrent and cross-process attempts are safe.
func (s *Store) Pair(code, name string, now time.Time) (string, Device, error) {
	deviceName, err := normalizeDeviceName(name)
	if err != nil {
		return "", Device{}, err
	}
	nowMs := now.UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return "", Device{}, fmt.Errorf("begin pair: %w", err)
	}
	// failAttempt commits the incremented attempt counter before reporting the
	// failure, so a wrong guess can never be lost by rolling back.
	failAttempt := func() (string, Device, error) {
		locked, err := bumpFailedAttempts(tx)
		if err != nil {
			tx.Rollback()
			return "", Device{}, err
		}
		if err := tx.Commit(); err != nil {
			return "", Device{}, fmt.Errorf("commit pairing attempt: %w", err)
		}
		if locked {
			return "", Device{}, ErrLocked
		}
		return "", Device{}, ErrInvalidCode
	}

	var failed int
	if err := tx.QueryRow(`SELECT failed_attempts FROM pairing_state WHERE id = ?`, pairingStateID).Scan(&failed); err != nil {
		tx.Rollback()
		return "", Device{}, fmt.Errorf("read pairing state: %w", err)
	}
	if failed >= maxFailed {
		tx.Rollback()
		return "", Device{}, ErrLocked
	}
	if !validCode(code) {
		return failAttempt()
	}
	res, err := tx.Exec(`UPDATE pairing_codes SET used_at = ? WHERE code_hash = ? AND used_at IS NULL AND expires_at > ?`,
		nowMs, hashSecret(code), nowMs)
	if err != nil {
		tx.Rollback()
		return "", Device{}, fmt.Errorf("consume pairing code: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		tx.Rollback()
		return "", Device{}, fmt.Errorf("consume pairing code: %w", err)
	}
	if affected != 1 {
		return failAttempt()
	}

	token, err := randomToken(tokenBytes)
	if err != nil {
		tx.Rollback()
		return "", Device{}, err
	}
	deviceID, err := randomToken(deviceIDBytes)
	if err != nil {
		tx.Rollback()
		return "", Device{}, err
	}
	if _, err := tx.Exec(`INSERT INTO paired_devices(id, name, token_hash, created_at, last_seen_at) VALUES(?, ?, ?, ?, ?)`,
		deviceID, deviceName, hashSecret(token), nowMs, nowMs); err != nil {
		tx.Rollback()
		return "", Device{}, fmt.Errorf("store paired device: %w", err)
	}
	if _, err := tx.Exec(`UPDATE pairing_state SET failed_attempts = 0 WHERE id = ?`, pairingStateID); err != nil {
		tx.Rollback()
		return "", Device{}, fmt.Errorf("reset pairing attempts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", Device{}, fmt.Errorf("commit paired device: %w", err)
	}
	return token, Device{ID: deviceID, Name: deviceName, CreatedAt: nowMs, LastSeenAt: nowMs}, nil
}

// LookupToken resolves a raw device token to its device and refreshes
// last_seen_at. Any unknown or revoked token yields ErrUnauthorized without
// revealing why.
func (s *Store) LookupToken(token string, now time.Time) (Device, error) {
	if token == "" {
		return Device{}, ErrUnauthorized
	}
	var dev Device
	err := s.db.QueryRow(`SELECT id, name, created_at, last_seen_at FROM paired_devices
		WHERE token_hash = ? AND revoked_at IS NULL`, hashSecret(token)).
		Scan(&dev.ID, &dev.Name, &dev.CreatedAt, &dev.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrUnauthorized
	}
	if err != nil {
		return Device{}, fmt.Errorf("lookup device token: %w", err)
	}
	nowMs := now.UnixMilli()
	// Best-effort bookkeeping: a failed touch must not invalidate a good token.
	_, _ = s.db.Exec(`UPDATE paired_devices SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`, nowMs, dev.ID)
	if nowMs > dev.LastSeenAt {
		dev.LastSeenAt = nowMs
	}
	return dev, nil
}

// ListDevices returns the active (non-revoked) devices, oldest first.
func (s *Store) ListDevices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id, name, created_at, last_seen_at FROM paired_devices
		WHERE revoked_at IS NULL ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	devices := make([]Device, 0, 4)
	for rows.Next() {
		var dev Device
		if err := rows.Scan(&dev.ID, &dev.Name, &dev.CreatedAt, &dev.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, dev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	return devices, nil
}

// RevokeDevice invalidates a device and its token. Unknown or already revoked
// IDs return ErrUnauthorized so callers cannot probe for device existence.
func (s *Store) RevokeDevice(id string) error {
	if id == "" {
		return ErrUnauthorized
	}
	res, err := s.db.Exec(`UPDATE paired_devices SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`,
		time.Now().UnixMilli(), id)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if affected != 1 {
		return ErrUnauthorized
	}
	return nil
}

// bumpFailedAttempts increments the hub-wide failure counter inside tx and
// reports whether the hub is now locked.
func bumpFailedAttempts(tx *sql.Tx) (bool, error) {
	if _, err := tx.Exec(`UPDATE pairing_state SET failed_attempts = failed_attempts + 1 WHERE id = ?`, pairingStateID); err != nil {
		return false, fmt.Errorf("record pairing attempt: %w", err)
	}
	var failed int
	if err := tx.QueryRow(`SELECT failed_attempts FROM pairing_state WHERE id = ?`, pairingStateID).Scan(&failed); err != nil {
		return false, fmt.Errorf("read pairing state: %w", err)
	}
	return failed >= maxFailed, nil
}

// normalizeDeviceName trims the display name and caps it at maxDeviceName
// runes. It never returns the raw error text to remote callers.
func normalizeDeviceName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errInvalidName
	}
	if utf8.RuneCountInString(trimmed) > maxDeviceName {
		runes := []rune(trimmed)
		trimmed = string(runes[:maxDeviceName])
	}
	return trimmed, nil
}

// randomCode returns exactly codeDigits decimal digits, zero padded, so values
// such as "0042" are possible.
func randomCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	return fmt.Sprintf("%0*d", codeDigits, n.Int64()), nil
}

func validCode(code string) bool {
	if len(code) != codeDigits {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// randomToken returns n crypto-random bytes encoded URL-safe without padding.
func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashSecret returns the lowercase hex SHA-256 of s; only hashes reach SQLite.
func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
