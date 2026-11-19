// Package token provides the random identifier and storage-hash primitives
// used for pairing codes, device tokens and item IDs. Raw tokens are never
// persisted; only Hash(token) is stored.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// Random returns a URL-safe random string carrying n bytes of entropy.
func Random(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Hash returns the storage key for a raw token: lowercase hex SHA-256.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
