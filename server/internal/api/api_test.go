// Full-server API tests: pairing authentication, items, uploads/downloads,
// WebSocket broadcast and the Janitor. Every request authenticates with a
// locally generated pairing code (see docs/PAIRING.md); no password route is
// exercised here. Pairing-specific HTTP/WS edge cases live in
// pairing_api_test.go.
package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"flowdrop/internal/auth"
	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/events"
	"flowdrop/internal/pairing"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
)

// Item is the store's item shape; aliased here for terse test assertions.
type Item = store.Item

type harness struct {
	t      *testing.T
	srv    *httptest.Server
	cfg    config.Config
	db     *sql.DB
	store  *store.Store
	files  *storage.Store
	broker *events.Broker
	api    *Server
	// pairing is the same store the server authenticates against, so tests can
	// generate local codes and revoke devices the way the desktop does.
	pairing *pairing.Store
	client  *http.Client
	csrf    string
	// deviceID/deviceToken identify the device registered by the last pair()
	// call; tests use them to assert the stored hash and revocation.
	deviceID    string
	deviceToken string
	// setCookies keeps the raw pairing response headers so tests can assert on
	// cookie attributes, which http.CookieJar drops.
	setCookies []string
}

// newHarness starts a full server on a real listener with a temp data dir.
func newHarness(t *testing.T, mutate ...func(*config.Config)) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:0"
	cfg.CookieSecure = false
	cfg.MaxUploadBytes = 1 << 20 // 1 MiB keeps upload tests fast
	cfg.MinFreeBytes = 0
	cfg.MaxConcurrentUploads = 2
	for _, m := range mutate {
		m(&cfg)
	}

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	files, err := storage.New(cfg.DataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	// The legacy password-session store is only the Janitor's expired-row
	// cleaner (see janitor.go). It holds no credential, is never consulted for
	// authentication, and is not populated here.
	legacySessions := auth.NewStore(conn, cfg.SessionTTL())
	pstore := pairing.NewStore(conn)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broker := events.NewBroker()
	st := store.New(conn)
	apiSrv := New(Deps{
		Config:   cfg,
		Sessions: legacySessions,
		Pairing:  pstore,
		Store:    st,
		Storage:  files,
		Broker:   broker,
		UI:       nil,
		Logger:   logger,
	})
	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("jar: %v", err)
	}
	h := &harness{
		t:       t,
		srv:     ts,
		cfg:     cfg,
		db:      conn,
		store:   st,
		files:   files,
		broker:  broker,
		api:     apiSrv,
		pairing: pstore,
		client:  &http.Client{Jar: jar, Timeout: 30 * time.Second},
	}
	return h
}

// newCode generates a local pairing code the way the Hub UI does.
func (h *harness) newCode() string {
	h.t.Helper()
	code, err := h.pairing.CreateCode(time.Now())
	if err != nil {
		h.t.Fatalf("create pairing code: %v", err)
	}
	return code.Code
}

// pair registers a browser device through POST /api/v1/auth/pair and keeps the
// CSRF token, device identity and raw cookies for later assertions.
func (h *harness) pair(mutate ...func(*http.Request)) {
	h.t.Helper()
	body := fmt.Sprintf(`{"code":%q,"device_name":"test-browser"}`, h.newCode())
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/v1/auth/pair", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.srv.URL)
	for _, m := range mutate {
		m(req)
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("pair: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("pair status = %d body=%s", res.StatusCode, b)
	}
	var out sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		h.t.Fatalf("decode pair: %v", err)
	}
	if !out.Authenticated || out.CSRFToken == "" || out.DeviceID == "" {
		h.t.Fatalf("pair response = %+v", out)
	}
	if out.DeviceToken != "" {
		h.t.Fatal("browser pairing returned a device token")
	}
	h.csrf = out.CSRFToken
	h.deviceID = out.DeviceID
	h.setCookies = res.Header.Values("Set-Cookie")
	u, _ := url.Parse(h.srv.URL)
	for _, c := range h.client.Jar.Cookies(u) {
		if c.Name == sessionCookie {
			h.deviceToken = c.Value
		}
	}
	if h.deviceToken == "" {
		h.t.Fatal("pairing issued no session cookie")
	}
}

func (h *harness) do(method, path string, body io.Reader, mutate ...func(*http.Request)) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, body)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	req.Header.Set("Origin", h.srv.URL)
	for _, m := range mutate {
		m(req)
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

func (h *harness) postJSON(path, body string) *http.Response {
	return h.do(http.MethodPost, path, strings.NewReader(body), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
}

func (h *harness) createText(kind, text, sender string) Item {
	h.t.Helper()
	body := fmt.Sprintf(`{"type":%q,"text":%q,"sender_device":%q}`, kind, text, sender)
	res := h.postJSON("/api/v1/items/text", body)
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("create text status = %d body=%s", res.StatusCode, b)
	}
	var it Item
	if err := json.NewDecoder(res.Body).Decode(&it); err != nil {
		h.t.Fatalf("decode item: %v", err)
	}
	return it
}

func (h *harness) list(cursor string, limit int) ([]Item, string) {
	h.t.Helper()
	path := fmt.Sprintf("/api/v1/items?limit=%d", limit)
	if cursor != "" {
		path += "&cursor=" + url.QueryEscape(cursor)
	}
	res := h.do(http.MethodGet, path, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("list status = %d body=%s", res.StatusCode, b)
	}
	var page struct {
		Items []Item `json:"items"`
		Next  string `json:"next_cursor"`
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		h.t.Fatalf("decode list: %v", err)
	}
	return page.Items, page.Next
}

func (h *harness) upload(name string, content []byte, sender string) (*http.Response, []byte) {
	h.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if sender != "" {
		_ = mw.WriteField("sender_device", sender)
	}
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		h.t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		h.t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		h.t.Fatalf("close mw: %v", err)
	}
	res := h.do(http.MethodPost, "/api/v1/items/upload", &buf, func(r *http.Request) {
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	return res, body
}

func decodeItem(t *testing.T, b []byte) Item {
	t.Helper()
	var it Item
	if err := json.Unmarshal(b, &it); err != nil {
		t.Fatalf("unmarshal item: %v body=%s", err, b)
	}
	return it
}

// ---------- authentication ----------

func TestUnauthenticatedAccessIsRejected(t *testing.T) {
	h := newHarness(t)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/items"},
		{http.MethodPost, "/api/v1/items/text"},
		{http.MethodPost, "/api/v1/items/upload"},
		{http.MethodGet, "/api/v1/events"},
		{http.MethodDelete, "/api/v1/items/whatever"},
		{http.MethodGet, "/api/v1/items/whatever/download"},
		{http.MethodGet, "/api/v1/items/whatever/preview"},
	}
	for _, c := range cases {
		res := h.do(c.method, c.path, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", c.method, c.path, res.StatusCode)
		}
	}
}

func TestHealthNeedsNoAuth(t *testing.T) {
	h := newHarness(t)
	res := h.do(http.MethodGet, "/api/v1/health", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health = %d", res.StatusCode)
	}
	var body map[string]any
	json.NewDecoder(res.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("health body = %v", body)
	}
	for _, leak := range []string{"password", "data_dir", "token"} {
		if _, ok := body[leak]; ok {
			t.Fatalf("health leaked %q", leak)
		}
	}
}

// TestPairWrongCodeIsUniform replaces the old wrong-password test: a wrong or
// already-consumed code answers the same 401 pairing_invalid without setting
// cookies, and one wrong guess does not burn a still-valid local code.
func TestPairWrongCodeIsUniform(t *testing.T) {
	h := newHarness(t)
	plain := &http.Client{Timeout: 10 * time.Second}

	code := h.newCode()
	wrong := "0000"
	if wrong == code {
		wrong = "0001"
	}
	send := func(c string) (*http.Response, []byte) {
		t.Helper()
		body := fmt.Sprintf(`{"code":%q,"device_name":"phone"}`, c)
		req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/v1/auth/pair", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", h.srv.URL)
		res, err := plain.Do(req)
		if err != nil {
			t.Fatalf("pair: %v", err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, raw
	}

	res, raw := send(wrong)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d body=%s, want 401", res.StatusCode, raw)
	}
	if got := decodePairError(t, raw); got != "pairing_invalid" {
		t.Fatalf("wrong code error = %q", got)
	}
	if bytes.Contains(raw, []byte(wrong)) || bytes.Contains(raw, []byte(code)) {
		t.Fatalf("pairing error echoed a code: %s", raw)
	}
	if n := len(res.Header.Values("Set-Cookie")); n != 0 {
		t.Fatalf("rejected pairing set %d cookies", n)
	}

	// The still-valid code survives the wrong attempt (a wrong guess counts
	// toward the hub-wide lock but does not invalidate the code itself).
	res, raw = send(code)
	if res.StatusCode != http.StatusOK || len(res.Header.Values("Set-Cookie")) == 0 {
		t.Fatalf("pair with live code = %d body=%s", res.StatusCode, raw)
	}

	// A consumed code is indistinguishable from a wrong one.
	res, raw = send(code)
	if res.StatusCode != http.StatusUnauthorized || decodePairError(t, raw) != "pairing_invalid" {
		t.Fatalf("reused code = %d body=%s", res.StatusCode, raw)
	}
}

// TestAPIRateLimitUsesAPILimitPerMinute pins that the HTTP limit is the generic
// API limiter (APILimitPerMinute), not the retired login limiter. Pairing's own
// hub-wide five-attempt lock is reported as pairing_locked and is covered in
// pairing_api_test.go, so it is deliberately not duplicated here.
func TestAPIRateLimitUsesAPILimitPerMinute(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.APILimitPerMinute = 2 })
	h.pair()

	for i := 1; i <= 2; i++ {
		res := h.do(http.MethodGet, "/api/v1/items?limit=1", nil)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", i, res.StatusCode)
		}
	}
	res := h.do(http.MethodGet, "/api/v1/items?limit=1", nil)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request 3 = %d body=%s, want 429", res.StatusCode, raw)
	}
	if code := decodePairError(t, raw); code != "rate_limited" {
		t.Fatalf("error code = %q, want rate_limited", code)
	}
}

func TestSessionCookieIsHttpOnlyAndHashed(t *testing.T) {
	h := newHarness(t)
	h.pair()

	var sessionSetCookie string
	for _, raw := range h.setCookies {
		if strings.HasPrefix(raw, sessionCookie+"=") {
			sessionSetCookie = raw
		}
	}
	if sessionSetCookie == "" {
		t.Fatalf("no session Set-Cookie header: %v", h.setCookies)
	}
	for _, attr := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(sessionSetCookie, attr) {
			t.Errorf("session cookie missing %q: %s", attr, sessionSetCookie)
		}
	}
	// The cookie value is the device token; only its hash may be persisted.
	token := h.deviceToken
	var stored string
	if err := h.db.QueryRow(`SELECT token_hash FROM paired_devices WHERE id = ?`, h.deviceID).Scan(&stored); err != nil {
		t.Fatalf("read paired device row: %v", err)
	}
	if stored == token {
		t.Fatal("plaintext device token stored in paired_devices")
	}
	if stored != auth.TokenID(token) {
		t.Fatal("stored token_hash is not the SHA-256 of the device token")
	}
}

func TestCSRFRequiredForWrites(t *testing.T) {
	h := newHarness(t)
	h.pair()

	// Missing token.
	res := h.do(http.MethodPost, "/api/v1/items/text",
		strings.NewReader(`{"text":"hi"}`), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
		})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF = %d, want 403", res.StatusCode)
	}

	// Wrong token.
	res = h.do(http.MethodPost, "/api/v1/items/text",
		strings.NewReader(`{"text":"hi"}`), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-FlowDrop-CSRF", "nope")
		})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong CSRF = %d, want 403", res.StatusCode)
	}

	// Wrong origin even with a valid token.
	res = h.do(http.MethodPost, "/api/v1/items/text",
		strings.NewReader(`{"text":"hi"}`), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-FlowDrop-CSRF", h.csrf)
			r.Header.Set("Origin", "https://evil.example")
		})
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin = %d, want 403", res.StatusCode)
	}
}

func TestLogoutRequiresCSRF(t *testing.T) {
	h := newHarness(t)
	h.pair()
	res := h.do(http.MethodPost, "/api/v1/auth/logout", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without CSRF = %d, want 403", res.StatusCode)
	}
	var n int
	h.db.QueryRow(`SELECT COUNT(*) FROM paired_devices WHERE revoked_at IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatalf("device was revoked by a CSRF-less request (active devices=%d)", n)
	}
}

func TestUnknownAPIPathDoesNotFallThroughToSPA(t *testing.T) {
	h := newHarness(t)
	h.pair()
	for _, path := range []string{"/api/v1/nope", "/api/v1/items/x/y/z", "/api/"} {
		res := h.do(http.MethodGet, path, nil)
		ct := res.Header.Get("Content-Type")
		res.Body.Close()
		if strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s returned HTML (%s); API 404s must stay JSON", path, ct)
		}
	}
}

func TestHostAllowlistRejectsUnknownHost(t *testing.T) {
	h := newHarness(t)
	res := h.do(http.MethodGet, "/api/v1/health", nil, func(r *http.Request) {
		r.Host = "attacker.example"
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("status = %d, want 421", res.StatusCode)
	}
}

func TestLogoutRevokesPairedDevice(t *testing.T) {
	h := newHarness(t)
	h.pair()
	token := h.deviceToken

	res := h.postJSON("/api/v1/auth/logout", "")
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d", res.StatusCode)
	}

	// Logout revokes instead of deleting: the row stays for local history and
	// the stored hash can no longer authenticate. Device tokens have no expiry
	// of their own (only the browser cookie has a lifetime); revocation is what
	// ends them.
	var active, total int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM paired_devices WHERE revoked_at IS NULL`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM paired_devices`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if active != 0 || total != 1 {
		t.Fatalf("active=%d total=%d after logout, want 0/1", active, total)
	}
	if _, err := h.pairing.LookupToken(token, time.Now()); err == nil {
		t.Fatal("revoked device token still authenticates")
	}
	res = h.do(http.MethodGet, "/api/v1/items", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("items after logout = %d, want 401", res.StatusCode)
	}
}

// ---------- items & pagination ----------

func TestTextAndLinkValidation(t *testing.T) {
	h := newHarness(t)
	h.pair()

	if it := h.createText("text", "hello 世界", "kubuntu"); it.Type != "text" || it.Text != "hello 世界" {
		t.Fatalf("unexpected item: %+v", it)
	}
	if it := h.createText("link", "https://example.com/a?b=1", ""); it.Type != "link" {
		t.Fatalf("link type = %q", it.Type)
	}

	bad := []string{
		`{"type":"link","text":"javascript:alert(1)"}`,
		`{"type":"link","text":"file:///etc/passwd"}`,
		`{"type":"link","text":"not a url"}`,
		`{"type":"text","text":"   "}`,
		`{"type":"image","text":"x"}`,
	}
	for _, body := range bad {
		res := h.postJSON("/api/v1/items/text", body)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", body, res.StatusCode)
		}
	}
}

func TestPaginationIsStableAndDescending(t *testing.T) {
	h := newHarness(t)
	h.pair()

	var want []string
	for i := 0; i < 12; i++ {
		it := h.createText("text", fmt.Sprintf("item-%02d", i), "t")
		want = append(want, it.ID)
	}
	var got []Item
	cursor := ""
	for pages := 0; ; pages++ {
		if pages > 20 {
			t.Fatal("pagination did not terminate")
		}
		items, next := h.list(cursor, 5)
		if len(items) > 5 {
			t.Fatalf("page returned %d items, limit was 5", len(items))
		}
		got = append(got, items...)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	seen := map[string]bool{}
	wantIDs := map[string]bool{}
	for _, id := range want {
		wantIDs[id] = true
	}
	for i, it := range got {
		if seen[it.ID] {
			t.Fatalf("duplicate id %s at position %d", it.ID, i)
		}
		seen[it.ID] = true
		if !wantIDs[it.ID] {
			t.Fatalf("unexpected id %s", it.ID)
		}
	}
	// Strictly descending by (created_at, id): this is the property the cursor
	// relies on, and it holds even when several items share a millisecond.
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.CreatedAt < cur.CreatedAt {
			t.Fatalf("page not descending by created_at at %d", i)
		}
		if prev.CreatedAt == cur.CreatedAt && prev.ID <= cur.ID {
			t.Fatalf("ties not ordered by id DESC at %d", i)
		}
	}
}

func TestInvalidCursorAndLimit(t *testing.T) {
	h := newHarness(t)
	h.pair()
	for _, q := range []string{"?cursor=!!!!", "?limit=0", "?limit=101", "?limit=abc"} {
		res := h.do(http.MethodGet, "/api/v1/items"+q, nil)
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("GET /items%s = %d, want 400", q, res.StatusCode)
		}
	}
}

// ---------- uploads ----------

func TestUploadImageIsPreviewableAndDownloadable(t *testing.T) {
	h := newHarness(t)
	h.pair()

	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40))
	res, body := h.upload("shot.png", png, "pixel")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d body=%s", res.StatusCode, body)
	}
	it := decodeItem(t, body)
	if it.Type != "image" {
		t.Fatalf("type = %q, want image", it.Type)
	}
	if it.Blob == nil || it.Blob.ContentType != "image/png" {
		t.Fatalf("blob = %+v", it.Blob)
	}

	// Preview: inline for safe raster images.
	pres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/preview", nil)
	pres.Body.Close()
	if ct := pres.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("preview content-type = %q", ct)
	}
	if cd := pres.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "inline") {
		t.Fatalf("preview disposition = %q", cd)
	}
	if pres.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff on preview")
	}

	// Download: always attachment.
	dres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/download", nil)
	got, _ := io.ReadAll(dres.Body)
	dres.Body.Close()
	if !bytes.Equal(got, png) {
		t.Fatalf("downloaded bytes differ: %d vs %d", len(got), len(png))
	}
	if cd := dres.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("download disposition = %q", cd)
	}
	if dres.Header.Get("X-Content-SHA256") != it.Blob.SHA256 {
		t.Fatal("sha256 header mismatch")
	}
}

func TestUploadActiveContentIsForcedAttachment(t *testing.T) {
	h := newHarness(t)
	h.pair()

	cases := []struct{ name, body string }{
		{"evil.html", "<html><script>alert(1)</script></html>"},
		{"evil.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`},
	}
	for _, c := range cases {
		res, body := h.upload(c.name, []byte(c.body), "")
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("%s upload = %d body=%s", c.name, res.StatusCode, body)
		}
		it := decodeItem(t, body)
		if it.Type != "file" {
			t.Errorf("%s type = %q, want file", c.name, it.Type)
		}
		pres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/preview", nil)
		pres.Body.Close()
		if ct := pres.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Errorf("%s preview content-type = %q, want octet-stream", c.name, ct)
		}
		if cd := pres.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
			t.Errorf("%s preview disposition = %q, want attachment", c.name, cd)
		}
	}
}

func TestUploadRejectsOversizeWithoutArtifact(t *testing.T) {
	h := newHarness(t)
	h.pair()

	big := bytes.Repeat([]byte("A"), int(h.cfg.MaxUploadBytes)+1024)
	res, body := h.upload("big.bin", big, "")
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("upload = %d body=%s, want 413", res.StatusCode, body)
	}
	items, _ := h.list("", 10)
	if len(items) != 0 {
		t.Fatalf("rejected upload created %d items", len(items))
	}
	var blobs int
	h.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&blobs)
	if blobs != 0 {
		t.Fatalf("rejected upload left %d blob rows", blobs)
	}
	assertNoFiles(t, filepath.Join(h.cfg.DataDir, "tmp"))
	assertNoFiles(t, filepath.Join(h.cfg.DataDir, "blobs"))
}

func TestUploadFilenameCannotEscapeStorage(t *testing.T) {
	h := newHarness(t)
	h.pair()

	res, body := h.upload("../../../etc/passwd", []byte("evil"), "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d body=%s", res.StatusCode, body)
	}
	it := decodeItem(t, body)
	if strings.ContainsAny(it.Blob.OriginalName, "/\\") {
		t.Fatalf("stored name retains path separators: %q", it.Blob.OriginalName)
	}
	// The on-disk path must be the random key, never the client name.
	entries, _ := filepath.Glob(filepath.Join(h.cfg.DataDir, "blobs", "*", "*"))
	if len(entries) != 1 {
		t.Fatalf("blob files = %v", entries)
	}
	if strings.Contains(entries[0], "passwd") {
		t.Fatalf("client filename leaked into storage path: %s", entries[0])
	}
	if !strings.HasSuffix(entries[0], it.Blob.ID[:0]+filepath.Base(entries[0])) {
		t.Fatal("unexpected path shape")
	}
}

func TestUploadExactlyAtLimitSucceeds(t *testing.T) {
	h := newHarness(t)
	h.pair()

	// A file exactly at the configured limit must be accepted: the multipart
	// framing overhead allowance must not turn "at the limit" into "over".
	exact := bytes.Repeat([]byte("Z"), int(h.cfg.MaxUploadBytes))
	res, body := h.upload("exact.bin", exact, "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload at limit = %d body=%s", res.StatusCode, body)
	}
	it := decodeItem(t, body)
	if it.Blob.SizeBytes != h.cfg.MaxUploadBytes {
		t.Fatalf("stored size = %d, want %d", it.Blob.SizeBytes, h.cfg.MaxUploadBytes)
	}
	dres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/download", nil)
	got, _ := io.ReadAll(dres.Body)
	dres.Body.Close()
	if len(got) != len(exact) {
		t.Fatalf("downloaded %d bytes, want %d", len(got), len(exact))
	}
}

func TestEmptyFileIsAllowed(t *testing.T) {
	h := newHarness(t)
	h.pair()
	res, body := h.upload("empty.txt", nil, "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d body=%s", res.StatusCode, body)
	}
	it := decodeItem(t, body)
	if it.Blob == nil || it.Blob.SizeBytes != 0 {
		t.Fatalf("blob = %+v", it.Blob)
	}
	dres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/download", nil)
	dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("download empty = %d", dres.StatusCode)
	}
}

func TestDownloadRequiresPairingEvenWithKnownID(t *testing.T) {
	h := newHarness(t)
	h.pair()
	res, body := h.upload("secret.txt", []byte("classified"), "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d", res.StatusCode)
	}
	it := decodeItem(t, body)

	// Drop cookies: knowledge of the item ID must not be enough.
	anonymous := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/items/"+it.ID+"/download", nil)
	ares, err := anonymous.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ares.Body.Close()
	if ares.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous download = %d, want 401", ares.StatusCode)
	}

	// A revoked device must also fail, even while its cookie is still present.
	if err := h.pairing.RevokeDevice(h.deviceID); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	dres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/download", nil)
	dres.Body.Close()
	if dres.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked-device download = %d, want 401", dres.StatusCode)
	}
}

func TestDeleteRemovesItemAndFile(t *testing.T) {
	h := newHarness(t)
	h.pair()
	res, body := h.upload("photo.png", []byte("\x89PNG\r\n\x1a\nabcdef"), "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d", res.StatusCode)
	}
	it := decodeItem(t, body)

	files, _ := filepath.Glob(filepath.Join(h.cfg.DataDir, "blobs", "*", "*"))
	if len(files) != 1 {
		t.Fatalf("expected 1 blob file, got %v", files)
	}

	dres := h.do(http.MethodDelete, "/api/v1/items/"+it.ID, nil, func(r *http.Request) {
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
	dres.Body.Close()
	if dres.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d", dres.StatusCode)
	}

	var items, blobs int
	h.db.QueryRow(`SELECT COUNT(*) FROM inbox_items`).Scan(&items)
	h.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&blobs)
	if items != 0 || blobs != 0 {
		t.Fatalf("items=%d blobs=%d after delete", items, blobs)
	}
	files, _ = filepath.Glob(filepath.Join(h.cfg.DataDir, "blobs", "*", "*"))
	if len(files) != 0 {
		t.Fatalf("blob files remain: %v", files)
	}

	// Second delete is a clean 404, not a 500.
	dres2 := h.do(http.MethodDelete, "/api/v1/items/"+it.ID, nil, func(r *http.Request) {
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
	dres2.Body.Close()
	if dres2.StatusCode != http.StatusNotFound {
		t.Fatalf("double delete = %d, want 404", dres2.StatusCode)
	}
}

// ---------- websocket ----------

func TestWebSocketRequiresAuth(t *testing.T) {
	h := newHarness(t)
	wsURL := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/events"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{h.srv.URL}},
	})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("unauthenticated WebSocket was accepted")
	}
	if res != nil && res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestWebSocketRejectsForeignOrigin(t *testing.T) {
	h := newHarness(t)
	h.pair()
	wsURL := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/events"

	var cookieHeader string
	u, _ := url.Parse(h.srv.URL)
	for _, c := range h.client.Jar.Cookies(u) {
		cookieHeader += c.Name + "=" + c.Value + "; "
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, res, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{"https://evil.example"},
			"Cookie": []string{cookieHeader},
		},
	})
	if err == nil {
		conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("foreign-origin WebSocket was accepted")
	}
	if res != nil && res.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", res.StatusCode)
	}
}

func TestWebSocketBroadcastsCreateAndDelete(t *testing.T) {
	h := newHarness(t)
	h.pair()
	wsURL := "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/events"

	var cookieHeader string
	u, _ := url.Parse(h.srv.URL)
	for _, c := range h.client.Jar.Cookies(u) {
		cookieHeader += c.Name + "=" + c.Value + "; "
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Origin": []string{h.srv.URL},
			"Cookie": []string{cookieHeader},
		},
	})
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var hello events.Event
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if hello.Type != "hello" {
		t.Fatalf("first frame = %+v, want hello", hello)
	}

	item := h.createText("text", "broadcast me", "t")

	var created events.Event
	if err := wsjson.Read(ctx, conn, &created); err != nil {
		t.Fatalf("read created: %v", err)
	}
	if created.Type != "item.created" || created.ID != item.ID {
		t.Fatalf("event = %+v", created)
	}

	dres := h.do(http.MethodDelete, "/api/v1/items/"+item.ID, nil, func(r *http.Request) {
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
	dres.Body.Close()

	var deleted events.Event
	if err := wsjson.Read(ctx, conn, &deleted); err != nil {
		t.Fatalf("read deleted: %v", err)
	}
	if deleted.Type != "item.deleted" || deleted.ID != item.ID {
		t.Fatalf("event = %+v", deleted)
	}
}

// ---------- persistence & cleanup ----------

func TestItemsSurviveRestart(t *testing.T) {
	h := newHarness(t)
	h.pair()
	it := h.createText("text", "persist me", "kubuntu")
	_, body := h.upload("keep.txt", []byte("payload"), "")
	uploaded := decodeItem(t, body)

	// Simulate a restart: close the test server, reopen DB from the same dir.
	h.srv.Close()
	h.db.Close()

	conn, err := db.Open(h.cfg.DataDir)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer conn.Close()
	st := store.New(conn)

	got, err := st.GetItem(it.ID)
	if err != nil {
		t.Fatalf("get text after restart: %v", err)
	}
	if got.Text != "persist me" {
		t.Fatalf("text = %q", got.Text)
	}
	gblob, err := st.GetItem(uploaded.ID)
	if err != nil {
		t.Fatalf("get file after restart: %v", err)
	}
	if gblob.Blob == nil || gblob.Blob.SHA256 != uploaded.Blob.SHA256 {
		t.Fatalf("blob after restart = %+v", gblob.Blob)
	}
	files, err := storage.New(h.cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := files.OpenBlob(gblob.Blob.StorageKey)
	if err != nil {
		t.Fatalf("blob file missing after restart: %v", err)
	}
	data, _ := io.ReadAll(f)
	f.Close()
	if string(data) != "payload" {
		t.Fatalf("blob content = %q", data)
	}
}

func TestJanitorRemovesOrphansAndStaleTemp(t *testing.T) {
	h := newHarness(t)
	h.pair()

	// A blob row whose item was deleted out from under it (simulating a crash
	// between item delete and blob delete).
	now := time.Now().Add(-2 * time.Hour).UnixMilli()
	key := strings.Repeat("ab", 16)
	h.db.Exec(`INSERT INTO blobs(id, original_name, content_type, size_bytes, sha256, storage_key, created_at)
		VALUES('orphanblob','x.txt','text/plain',3,'deadbeef',?,?)`, key, now)
	// Legacy password sessions are only cleaned up by the Janitor. This fixture
	// is explicit historical-table cleanup (not authentication): one expired row
	// that must go and one unexpired row that must stay.
	if _, err := h.db.Exec(`INSERT INTO admin_users(id, username, password_hash, created_at, updated_at)
		VALUES('legacyuser','legacy','unused',0,0)`); err != nil {
		t.Fatalf("insert legacy admin fixture: %v", err)
	}
	if _, err := h.db.Exec(`INSERT INTO sessions(id, user_id, created_at, last_seen_at, expires_at, user_agent)
		VALUES('expiredsession','legacyuser',0,0,1,''),
		      ('livesession','legacyuser',0,0,?, '')`, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("insert legacy session fixture: %v", err)
	}
	var legacyBefore int
	h.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&legacyBefore)
	if legacyBefore != 2 {
		t.Fatalf("legacy session fixture = %d rows, want 2", legacyBefore)
	}

	// A stale temp file and an in-flight one.
	tmpDir := filepath.Join(h.cfg.DataDir, "tmp")
	stale := filepath.Join(tmpDir, "stale.part")
	os.WriteFile(stale, []byte("junk"), 0o600)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(stale, old, old)

	live, livePath, err := h.files.NewTempFile()
	if err != nil {
		t.Fatal(err)
	}
	live.WriteString("in progress")

	j := &Janitor{
		Sessions:    h.api.sess,
		Store:       h.store,
		Storage:     h.files,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		GracePeriod: time.Minute,
		TempMaxAge:  time.Hour,
	}
	j.RunOnce(context.Background())

	var blobs int
	h.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&blobs)
	if blobs != 0 {
		t.Fatalf("orphan blob rows remain: %d", blobs)
	}
	var sessions int
	h.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id='expiredsession'`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("expired legacy session was not removed")
	}
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id='livesession'`).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 1 {
		t.Fatal("janitor removed an unexpired legacy session")
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale temp file was not removed")
	}
	if _, err := os.Stat(livePath); err != nil {
		t.Fatal("in-flight temp file was removed by the janitor")
	}

	live.Close()
	h.files.RemoveTemp(livePath)
}

func TestJanitorRemovesUnreferencedBlobFiles(t *testing.T) {
	h := newHarness(t)
	h.pair()
	// Write a file to blob storage with no DB row at all.
	key := strings.Repeat("cd", 16)
	if _, err := h.files.WriteReader(strings.NewReader("ghost"), key); err != nil {
		t.Fatal(err)
	}
	p, _ := h.files.BlobPath(key)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(p, old, old)

	j := &Janitor{
		Sessions:    h.api.sess,
		Store:       h.store,
		Storage:     h.files,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		GracePeriod: time.Minute,
		TempMaxAge:  time.Hour,
	}
	j.RunOnce(context.Background())

	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("unreferenced blob file was not removed")
	}
}

func TestJanitorKeepsCommittedBlobs(t *testing.T) {
	h := newHarness(t)
	h.pair()
	res, body := h.upload("keep.bin", []byte("keep me"), "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d", res.StatusCode)
	}
	it := decodeItem(t, body)

	// Make it look old so the grace period cannot be the reason it survives.
	h.db.Exec(`UPDATE blobs SET created_at = ? WHERE id = ?`, time.Now().Add(-48*time.Hour).UnixMilli(), it.Blob.ID)
	// storage_key is not exposed through the API; read it from the DB.
	var storageKey string
	if err := h.db.QueryRow(`SELECT storage_key FROM blobs WHERE id = ?`, it.Blob.ID).Scan(&storageKey); err != nil {
		t.Fatalf("read storage key: %v", err)
	}
	p, err := h.files.BlobPath(storageKey)
	if err != nil {
		t.Fatalf("blob path: %v", err)
	}
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(p, old, old)

	j := &Janitor{
		Sessions: h.api.sess, Store: h.store, Storage: h.files,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		GracePeriod: time.Minute, TempMaxAge: time.Hour,
	}
	j.RunOnce(context.Background())

	if _, err := os.Stat(p); err != nil {
		t.Fatal("janitor removed a blob that is still referenced")
	}
	dres := h.do(http.MethodGet, "/api/v1/items/"+it.ID+"/download", nil)
	defer dres.Body.Close()
	if dres.StatusCode != http.StatusOK {
		t.Fatalf("download after janitor = %d", dres.StatusCode)
	}
}

// ---------- helpers ----------

func assertNoFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read %s: %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s should be empty, found %v", dir, names)
	}
}
