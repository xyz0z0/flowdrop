package pairing

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"flowdrop/internal/db"
)

var baseTime = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

// openStore opens a real SQLite database in a temp dir and migrates it.
func openStore(t *testing.T, dataDir string) (*Store, *sql.DB) {
	t.Helper()
	conn, err := db.Open(dataDir)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return NewStore(conn), conn
}

func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	return openStore(t, filepath.Join(t.TempDir(), "data"))
}

func TestCreateCodeFourDigitsWithLeadingZeros(t *testing.T) {
	s, _ := newStore(t)
	sawLeadingZero := false
	for i := 0; i < 200; i++ {
		code, err := s.CreateCode(baseTime)
		if err != nil {
			t.Fatalf("create code: %v", err)
		}
		if len(code.Code) != 4 {
			t.Fatalf("code %q len = %d, want 4", code.Code, len(code.Code))
		}
		if !validCode(code.Code) {
			t.Fatalf("code %q is not 4 decimal digits", code.Code)
		}
		if code.Code[0] == '0' {
			sawLeadingZero = true
		}
		wantExpiry := baseTime.Add(120 * time.Second).UnixMilli()
		if code.ExpiresAt != wantExpiry {
			t.Fatalf("expires_at = %d, want %d", code.ExpiresAt, wantExpiry)
		}
	}
	if !sawLeadingZero {
		t.Fatal("200 codes never started with 0; zero padding looks broken")
	}
}

func TestPairSucceedsOnce(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	token, dev, err := s.Pair(code.Code, " Pixel 9 ", baseTime.Add(time.Second))
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if token == "" || dev.ID == "" {
		t.Fatalf("empty token/id: %+v", dev)
	}
	if dev.Name != "Pixel 9" {
		t.Fatalf("name = %q, want trimmed", dev.Name)
	}
	if _, _, err := s.Pair(code.Code, "second", baseTime.Add(2*time.Second)); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("second pair err = %v, want ErrInvalidCode", err)
	}
	if _, err := s.LookupToken(token, baseTime.Add(3*time.Second)); err != nil {
		t.Fatalf("lookup token: %v", err)
	}
}

func TestPairRejectsExpiredCode(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	if _, _, err := s.Pair(code.Code, "dev", baseTime.Add(120*time.Second)); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("pair at TTL err = %v, want ErrInvalidCode", err)
	}
}

func TestPairGlobalLockAfterFiveWrongCodes(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	wrong := "not-a-code"
	for i := 0; i < 4; i++ {
		if _, _, err := s.Pair(wrong, "dev", baseTime); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("attempt %d err = %v, want ErrInvalidCode", i+1, err)
		}
	}
	if _, _, err := s.Pair(wrong, "dev", baseTime); !errors.Is(err, ErrLocked) {
		t.Fatalf("5th attempt err = %v, want ErrLocked", err)
	}
	// The correct code must no longer work, and repeated calls stay locked.
	if _, _, err := s.Pair(code.Code, "dev", baseTime); !errors.Is(err, ErrLocked) {
		t.Fatalf("correct code while locked err = %v, want ErrLocked", err)
	}
	if _, _, err := s.Pair(code.Code, "dev", baseTime); !errors.Is(err, ErrLocked) {
		t.Fatalf("still locked err = %v, want ErrLocked", err)
	}
	// Local regeneration is the only unlock path.
	fresh, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create fresh code: %v", err)
	}
	if _, _, err := s.Pair(fresh.Code, "dev", baseTime); err != nil {
		t.Fatalf("pair after regeneration: %v", err)
	}
}

func TestNewCodeInvalidatesOld(t *testing.T) {
	s, _ := newStore(t)
	old, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create old: %v", err)
	}
	fresh, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create fresh: %v", err)
	}
	if old.Code == fresh.Code {
		t.Fatalf("regenerated code %q equals the code it replaced; old value would stay usable", fresh.Code)
	}
	if _, _, err := s.Pair(old.Code, "dev", baseTime); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("old code err = %v, want ErrInvalidCode", err)
	}
	if _, _, err := s.Pair(fresh.Code, "dev", baseTime); err != nil {
		t.Fatalf("fresh code: %v", err)
	}
}

// TestCreateCodeSkipsCollidingCandidate forces the first candidate to equal
// the previous code and proves the stored/generated code is the second one.
func TestCreateCodeSkipsCollidingCandidate(t *testing.T) {
	s, conn := newStore(t)
	prev, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create previous: %v", err)
	}
	candidates := []string{prev.Code, "4321"}
	calls := 0
	fresh, err := s.createCode(baseTime, func() (string, error) {
		if calls >= len(candidates) {
			return "0000", nil
		}
		c := candidates[calls]
		calls++
		return c, nil
	})
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	if calls != 2 {
		t.Fatalf("generator calls = %d, want 2: the colliding candidate must be rejected", calls)
	}
	if fresh.Code != "4321" {
		t.Fatalf("code = %q, want the non-colliding second candidate", fresh.Code)
	}
	if fresh.Code == prev.Code {
		t.Fatal("regenerated code equals previous code")
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes`).Scan(&n); err != nil {
		t.Fatalf("count codes: %v", err)
	}
	if n != 1 {
		t.Fatalf("pairing_codes rows = %d, want 1", n)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes WHERE code_hash = ?`, hashSecret(prev.Code)).Scan(&n); err != nil {
		t.Fatalf("count previous hash: %v", err)
	}
	if n != 0 {
		t.Fatal("previous code hash still stored")
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes WHERE code_hash = ?`, hashSecret(fresh.Code)).Scan(&n); err != nil {
		t.Fatalf("count fresh hash: %v", err)
	}
	if n != 1 {
		t.Fatalf("fresh code hash rows = %d, want 1", n)
	}
	if _, _, err := s.Pair(prev.Code, "dev", baseTime); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("previous code err = %v, want ErrInvalidCode", err)
	}
	if _, _, err := s.Pair(fresh.Code, "dev", baseTime); err != nil {
		t.Fatalf("fresh code: %v", err)
	}
}

// TestCreateCodeFailureKeepsPreviousCodeAndLock proves a failed generation
// rolls back atomically: the old code and the hub-wide lock survive, and the
// stored hash is still the old one.
func TestCreateCodeFailureKeepsPreviousCodeAndLock(t *testing.T) {
	cases := []struct {
		name string
		gen  func(prev string) (func() (string, error), *int)
	}{
		{
			name: "generator error",
			gen: func(prev string) (func() (string, error), *int) {
				calls := 0
				return func() (string, error) {
					calls++
					return "", errors.New("entropy unavailable")
				}, &calls
			},
		},
		{
			name: "every candidate collides",
			gen: func(prev string) (func() (string, error), *int) {
				calls := 0
				return func() (string, error) {
					calls++
					return prev, nil
				}, &calls
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, conn := newStore(t)
			old, err := s.CreateCode(baseTime)
			if err != nil {
				t.Fatalf("create old: %v", err)
			}
			// Lock the hub, so losing the lock state would be observable.
			for i := 0; i < maxFailed; i++ {
				_, _, _ = s.Pair("bad", "dev", baseTime)
			}
			gen, calls := tc.gen(old.Code)
			if _, err := s.createCode(baseTime, gen); err == nil {
				t.Fatal("create code succeeded, want failure")
			}
			if tc.name == "every candidate collides" && *calls != maxCodeAttempts {
				t.Fatalf("generator calls = %d, want %d", *calls, maxCodeAttempts)
			}
			var n int
			if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes`).Scan(&n); err != nil {
				t.Fatalf("count codes: %v", err)
			}
			if n != 1 {
				t.Fatalf("pairing_codes rows = %d, want the previous code kept", n)
			}
			if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes WHERE code_hash = ?`, hashSecret(old.Code)).Scan(&n); err != nil {
				t.Fatalf("count old hash: %v", err)
			}
			if n != 1 {
				t.Fatal("previous code hash lost after failed generation")
			}
			var failed int
			if err := conn.QueryRow(`SELECT failed_attempts FROM pairing_state WHERE id = 1`).Scan(&failed); err != nil {
				t.Fatalf("read attempts: %v", err)
			}
			if failed != maxFailed {
				t.Fatalf("failed_attempts = %d, want lock preserved at %d", failed, maxFailed)
			}
			if _, _, err := s.Pair(old.Code, "dev", baseTime); !errors.Is(err, ErrLocked) {
				t.Fatalf("old code err = %v, want ErrLocked preserved", err)
			}
			// A real regeneration still unlocks and replaces the code.
			fresh, err := s.CreateCode(baseTime)
			if err != nil {
				t.Fatalf("recover with fresh code: %v", err)
			}
			if fresh.Code == old.Code {
				t.Fatal("recovered code equals previous code")
			}
			if _, _, err := s.Pair(fresh.Code, "dev", baseTime); err != nil {
				t.Fatalf("pair with recovered code: %v", err)
			}
		})
	}
}

func TestConcurrentPairOnlyOneWins(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	const workers = 8
	var wg sync.WaitGroup
	results := make([]error, workers)
	tokens := make([]string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], _, results[i] = s.Pair(code.Code, "dev", baseTime)
		}(i)
	}
	wg.Wait()
	success := 0
	for i, err := range results {
		switch {
		case err == nil:
			success++
			if tokens[i] == "" {
				t.Fatal("winner got empty token")
			}
		case errors.Is(err, ErrInvalidCode), errors.Is(err, ErrLocked):
		default:
			t.Fatalf("worker %d unexpected err: %v", i, err)
		}
	}
	if success != 1 {
		t.Fatalf("successful pairs = %d, want exactly 1", success)
	}
	devices, err := s.ListDevices()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(devices))
	}
}

func TestConcurrentWrongAttemptsRespectCap(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	wrong := "0000"
	if code.Code == wrong {
		wrong = "1111"
	}
	const workers = 12
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = s.Pair(wrong, "dev", baseTime)
		}()
	}
	wg.Wait()
	var failed int
	if err := s.db.QueryRow(`SELECT failed_attempts FROM pairing_state WHERE id = 1`).Scan(&failed); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if failed != maxFailed {
		t.Fatalf("failed_attempts = %d, want capped at %d", failed, maxFailed)
	}
	if _, _, err := s.Pair(wrong, "dev", baseTime); !errors.Is(err, ErrLocked) {
		t.Fatalf("post-cap err = %v, want ErrLocked", err)
	}
}

func TestReopenKeepsHubIDAndToken(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	s, conn := openStore(t, dataDir)
	hubID, err := s.HubID()
	if err != nil {
		t.Fatalf("hub id: %v", err)
	}
	if hubID == "" {
		t.Fatal("empty hub id")
	}
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	token, dev, err := s.Pair(code.Code, "dev", baseTime)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	conn.Close()

	s2, _ := openStore(t, dataDir)
	hubID2, err := s2.HubID()
	if err != nil {
		t.Fatalf("hub id after reopen: %v", err)
	}
	if hubID2 != hubID {
		t.Fatalf("hub id changed on reopen: %q -> %q", hubID, hubID2)
	}
	got, err := s2.LookupToken(token, baseTime.Add(time.Minute))
	if err != nil {
		t.Fatalf("lookup after reopen: %v", err)
	}
	if got.ID != dev.ID || got.Name != dev.Name {
		t.Fatalf("device after reopen = %+v, want %+v", got, dev)
	}
}

func TestHubIDConcurrentInit(t *testing.T) {
	s, _ := newStore(t)
	const workers = 8
	ids := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = s.HubID()
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("hub ids diverged: %q vs %q", ids[i], ids[0])
		}
	}
}

func TestNoRawSecretsInDatabase(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	s, conn := openStore(t, dataDir)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	token, dev, err := s.Pair(code.Code, "dev", baseTime)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}

	// Logical check: no column equals the raw code or token.
	var hits int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes WHERE code_hash = ?`, code.Code).Scan(&hits); err != nil {
		t.Fatalf("query codes: %v", err)
	}
	if hits != 0 {
		t.Fatal("raw pairing code found in pairing_codes")
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM paired_devices WHERE token_hash = ? OR id = ?`, token, token).Scan(&hits); err != nil {
		t.Fatalf("query devices: %v", err)
	}
	if hits != 0 {
		t.Fatal("raw device token found in paired_devices")
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM pairing_codes WHERE code_hash = ?`, hashSecret(code.Code)).Scan(&hits); err != nil {
		t.Fatalf("query hashed code: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hashed code rows = %d, want 1", hits)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM paired_devices WHERE token_hash = ? AND id = ?`, hashSecret(token), dev.ID).Scan(&hits); err != nil {
		t.Fatalf("query hashed token: %v", err)
	}
	if hits != 1 {
		t.Fatalf("hashed token rows = %d, want 1", hits)
	}

	// Physical check: the raw token must not appear anywhere in the SQLite
	// files, while its hash must.
	conn.Close()
	var all []byte
	for _, name := range []string{"flowdrop.db", "flowdrop.db-wal"} {
		raw, err := os.ReadFile(filepath.Join(dataDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("read %s: %v", name, err)
		}
		all = append(all, raw...)
	}
	if len(all) == 0 {
		t.Fatal("read no sqlite files")
	}
	if bytes.Contains(all, []byte(token)) {
		t.Fatal("raw device token found in sqlite files")
	}
	if !bytes.Contains(all, []byte(hashSecret(token))) {
		t.Fatal("token hash missing from sqlite files")
	}
}

func TestLookupTokenRejectsUnknownAndEmpty(t *testing.T) {
	s, _ := newStore(t)
	for _, tok := range []string{"", "unknown-token", strings.Repeat("x", 43)} {
		if _, err := s.LookupToken(tok, baseTime); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("lookup %q err = %v, want ErrUnauthorized", tok, err)
		}
	}
}

func TestRevokeInvalidatesTokenAndHidesDevice(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	token, dev, err := s.Pair(code.Code, "dev", baseTime)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if err := s.RevokeDevice(dev.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.LookupToken(token, baseTime.Add(time.Minute)); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("lookup revoked err = %v, want ErrUnauthorized", err)
	}
	devices, err := s.ListDevices()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices after revoke = %+v, want none", devices)
	}
	// Unknown and already-revoked IDs must not be distinguishable.
	if err := s.RevokeDevice(dev.ID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoke twice err = %v, want ErrUnauthorized", err)
	}
	if err := s.RevokeDevice("nope"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoke unknown err = %v, want ErrUnauthorized", err)
	}
	if err := s.RevokeDevice(""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoke empty err = %v, want ErrUnauthorized", err)
	}
}

func TestListDevicesAndNameCap(t *testing.T) {
	s, _ := newStore(t)
	longName := strings.Repeat("d", 70)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	_, first, err := s.Pair(code.Code, longName, baseTime)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	if len([]rune(first.Name)) != 64 {
		t.Fatalf("long name len = %d runes, want 64", len([]rune(first.Name)))
	}
	code2, err := s.CreateCode(baseTime.Add(time.Second))
	if err != nil {
		t.Fatalf("create code 2: %v", err)
	}
	_, second, err := s.Pair(code2.Code, "laptop", baseTime.Add(time.Second))
	if err != nil {
		t.Fatalf("pair 2: %v", err)
	}
	devices, err := s.ListDevices()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(devices))
	}
	if devices[0].ID != first.ID || devices[1].ID != second.ID {
		t.Fatalf("order = %q,%q want %q,%q", devices[0].ID, devices[1].ID, first.ID, second.ID)
	}
	code3, err := s.CreateCode(baseTime.Add(2 * time.Second))
	if err != nil {
		t.Fatalf("create code 3: %v", err)
	}
	if _, _, err := s.Pair(code3.Code, "   ", baseTime.Add(2*time.Second)); err == nil {
		t.Fatal("empty device name accepted")
	}
	// The failed name check must not consume the code.
	if _, _, err := s.Pair(code3.Code, "tablet", baseTime.Add(2*time.Second)); err != nil {
		t.Fatalf("pair after empty-name rejection: %v", err)
	}
}

func TestLookupTokenRefreshesLastSeen(t *testing.T) {
	s, _ := newStore(t)
	code, err := s.CreateCode(baseTime)
	if err != nil {
		t.Fatalf("create code: %v", err)
	}
	token, _, err := s.Pair(code.Code, "dev", baseTime)
	if err != nil {
		t.Fatalf("pair: %v", err)
	}
	later := baseTime.Add(5 * time.Minute)
	dev, err := s.LookupToken(token, later)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if dev.LastSeenAt != later.UnixMilli() {
		t.Fatalf("last_seen = %d, want %d", dev.LastSeenAt, later.UnixMilli())
	}
	devices, err := s.ListDevices()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if devices[0].LastSeenAt != later.UnixMilli() {
		t.Fatalf("stored last_seen = %d, want %d", devices[0].LastSeenAt, later.UnixMilli())
	}
}
