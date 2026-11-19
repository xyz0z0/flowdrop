package token

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestRandomShape(t *testing.T) {
	const n = 32
	tok := Random(n)
	if want := base64.RawURLEncoding.EncodedLen(n); len(tok) != want {
		t.Fatalf("Random(%d) length = %d, want %d", n, len(tok), want)
	}
	for _, r := range tok {
		if !strings.ContainsRune("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_", r) {
			t.Fatalf("Random(%d) contains non-URL-safe character %q", n, r)
		}
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(tok); err != nil || len(decoded) != n {
		t.Fatalf("Random(%d) is not valid base64url of %d bytes: %v", n, n, err)
	}
}

func TestRandomIsNotRepeated(t *testing.T) {
	if a, b := Random(16), Random(16); a == b {
		t.Fatal("two Random(16) calls returned the same value")
	}
}

func TestHashMatchesSHA256(t *testing.T) {
	const raw = "device-token-abc"
	sum := sha256.Sum256([]byte(raw))
	if got, want := Hash(raw), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("Hash(%q) = %q, want %q", raw, got, want)
	}
	if len(Hash("")) != 64 {
		t.Fatal("Hash(\"\") is not a 64-character hex digest")
	}
	if Hash(raw) == Hash(raw+"x") {
		t.Fatal("Hash is not sensitive to input")
	}
}
