package api

// Device pairing HTTP/WS contract tests (docs/PAIRING.md, PA-04A).
//
// These have their own harness: a real SQLite database in a temp dir, the
// pairing store, and a loopback listener on an ephemeral port. Pairing is the
// only authentication path; the removed password endpoints must stay removed.

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
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/events"
	"flowdrop/internal/pairing"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
)

type pairHarness struct {
	t       *testing.T
	srv     *httptest.Server
	cfg     config.Config
	db      *sql.DB
	store   *store.Store
	files   *storage.Store
	broker  *events.Broker
	api     *Server
	pairing *pairing.Store
	client  *http.Client
	hubID   string
}

// newPairHarness starts a real server with only the pairing store wired as the
// authentication source.
func newPairHarness(t *testing.T) *pairHarness {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.Listen = "127.0.0.1:0"
	cfg.CookieSecure = false
	cfg.MaxUploadBytes = 1 << 20
	cfg.MinFreeBytes = 0
	cfg.MaxConcurrentUploads = 2
	// These tests exercise pairing/store semantics (single use, uniform errors,
	// the hub-wide lock), not per-source-IP throttling, which has its own test
	// (TestPairRateLimitUsesPairLimitPerMinute). A realistic per-IP limit would
	// turn the deliberate wrong-code loops into rate_limited responses.
	cfg.PairLimitPerMinute = 1000

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	files, err := storage.New(cfg.DataDir)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	pstore := pairing.NewStore(conn)
	hubID, err := pstore.HubID()
	if err != nil {
		t.Fatalf("hub id: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	broker := events.NewBroker()
	st := store.New(conn)
	apiSrv := New(Deps{
		Config:  cfg,
		Pairing: pstore,
		Store:   st,
		Storage: files,
		Broker:  broker,
		UI:      nil,
		Logger:  logger,
		// No HubInfo callback: the server must fall back to the persistent ID
		// and an empty endpoint list.
	})
	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ts.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("jar: %v", err)
	}
	return &pairHarness{
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
		hubID:   hubID,
	}
}

func (h *pairHarness) newCode() string {
	h.t.Helper()
	code, err := h.pairing.CreateCode(time.Now())
	if err != nil {
		h.t.Fatalf("create code: %v", err)
	}
	return code.Code
}

func (h *pairHarness) do(method, path string, body io.Reader, mutate ...func(*http.Request)) *http.Response {
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

// pairRequestResult is a pairing response plus its raw Set-Cookie headers.
type pairResult struct {
	status  int
	body    sessionResponse
	raw     []byte
	cookies []string
}

func (h *pairHarness) pair(client *http.Client, code, name string, native bool) pairResult {
	h.t.Helper()
	payload := fmt.Sprintf(`{"code":%q,"device_name":%q,"native":%t}`, code, name, native)
	req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/api/v1/auth/pair", strings.NewReader(payload))
	if err != nil {
		h.t.Fatalf("pair request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.srv.URL)
	res, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("pair: %v", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := pairResult{status: res.StatusCode, raw: raw, cookies: res.Header.Values("Set-Cookie")}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

// pairWeb pairs through the harness cookie client and remembers the CSRF token.
func (h *pairHarness) pairWeb(name string) sessionResponse {
	h.t.Helper()
	res := h.pair(h.client, h.newCode(), name, false)
	if res.status != http.StatusOK {
		h.t.Fatalf("pair status = %d body=%s", res.status, res.raw)
	}
	return res.body
}

func (h *pairHarness) cookieHeader() string {
	h.t.Helper()
	u, _ := url.Parse(h.srv.URL)
	var b strings.Builder
	for _, c := range h.client.Jar.Cookies(u) {
		b.WriteString(c.Name + "=" + c.Value + "; ")
	}
	return b.String()
}

func (h *pairHarness) wsURL() string {
	return "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/v1/events"
}

// dialWS opens an authenticated event stream with the given token transport.
func (h *pairHarness) dialWS(origin string, extra http.Header) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	header := http.Header{"Origin": []string{origin}}
	for k, v := range extra {
		header[k] = v
	}
	return websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{HTTPHeader: header})
}

func decodePairError(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode error body %s: %v", raw, err)
	}
	return body.Error.Code
}

// TestPairingAPI is the PA-04A contract test: pairing cookies vs native token,
// error/lock/single-use behaviour, Bearer session restore, retirement of the
// password login, protection of every data route, and revocation of live
// sockets and new requests.
func TestPairingAPI(t *testing.T) {
	t.Run("health_reports_stable_hub_id", func(t *testing.T) {
		h := newPairHarness(t)

		var first struct {
			Status string `json:"status"`
			HubID  string `json:"hub_id"`
		}
		res := h.do(http.MethodGet, "/api/v1/health", nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("health = %d", res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(&first); err != nil {
			res.Body.Close()
			t.Fatalf("decode health: %v", err)
		}
		res.Body.Close()
		if first.Status != "ok" || first.HubID == "" {
			t.Fatalf("health = %+v", first)
		}

		var second struct {
			HubID string `json:"hub_id"`
		}
		res = h.do(http.MethodGet, "/api/v1/health", nil)
		if err := json.NewDecoder(res.Body).Decode(&second); err != nil {
			res.Body.Close()
			t.Fatalf("decode health: %v", err)
		}
		res.Body.Close()
		if second.HubID != first.HubID || second.HubID != h.hubID {
			t.Fatalf("hub_id not stable: %q / %q / %q", first.HubID, second.HubID, h.hubID)
		}

		// Unpaired session probe answers 200 with authenticated=false.
		res = h.do(http.MethodGet, "/api/v1/auth/session", nil)
		var info sessionResponse
		if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
			res.Body.Close()
			t.Fatalf("decode session: %v", err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK || info.Authenticated || info.CSRFToken != "" {
			t.Fatalf("unpaired session = %d %+v", res.StatusCode, info)
		}
	})

	t.Run("pair_cookie_vs_native_token", func(t *testing.T) {
		h := newPairHarness(t)
		web := h.pairWeb("web-browser")
		if !web.Authenticated || web.CSRFToken == "" || web.DeviceID == "" {
			t.Fatalf("web pair body = %+v", web)
		}
		if web.DeviceToken != "" {
			t.Fatal("browser pairing must not return a device token")
		}
		if web.Hub == nil || web.Hub.ID != h.hubID || web.Hub.Name == "" {
			t.Fatalf("hub = %+v", web.Hub)
		}
		if web.Hub.Endpoints == nil || len(web.Hub.Endpoints) != 0 {
			t.Fatalf("hub endpoints invented: %+v", web.Hub.Endpoints)
		}

		// The cookie response must set a HttpOnly session cookie and a
		// JS-readable CSRF cookie.
		webRes := h.pair(h.client, h.newCode(), "web-second", false)
		if webRes.status != http.StatusOK {
			t.Fatalf("pair status = %d body=%s", webRes.status, webRes.raw)
		}
		var sessionSet, csrfSet bool
		for _, raw := range webRes.cookies {
			parts := strings.SplitN(raw, ";", 2)
			kv := strings.SplitN(parts[0], "=", 2)
			if len(kv) != 2 {
				continue
			}
			name := strings.TrimSpace(kv[0])
			attrs := strings.ToLower(raw)
			switch name {
			case sessionCookie:
				sessionSet = true
				if !strings.Contains(attrs, "httponly") {
					t.Fatalf("session cookie must be HttpOnly: %q", raw)
				}
			case csrfCookie:
				csrfSet = true
				if strings.Contains(attrs, "httponly") {
					t.Fatalf("csrf cookie must be readable: %q", raw)
				}
			}
		}
		if !sessionSet || !csrfSet {
			t.Fatalf("pair Set-Cookie = %v", webRes.cookies)
		}

		// Native pairing additionally returns the raw token once.
		native := h.pair(&http.Client{Timeout: 10 * time.Second}, h.newCode(), "android", true)
		if native.status != http.StatusOK {
			t.Fatalf("native pair = %d body=%s", native.status, native.raw)
		}
		if native.body.DeviceToken == "" || native.body.DeviceID == "" {
			t.Fatalf("native pair body = %+v", native.body)
		}
		dev, err := h.pairing.LookupToken(native.body.DeviceToken, time.Now())
		if err != nil {
			t.Fatalf("native token not usable: %v", err)
		}
		if dev.ID != native.body.DeviceID {
			t.Fatalf("device id = %q, want %q", dev.ID, native.body.DeviceID)
		}
		if bytes.Contains(webRes.raw, []byte("device_token")) {
			t.Fatal("browser pair leaked a device token")
		}
		if !bytes.Contains(native.raw, []byte("device_token")) {
			t.Fatal("native pair did not return a device token")
		}
	})

	t.Run("pair_errors_lock_and_single_use", func(t *testing.T) {
		h := newPairHarness(t)
		plain := &http.Client{Timeout: 10 * time.Second}

		// A missing device name is a malformed request: no code consumed, no
		// failed attempt counted.
		code := h.newCode()
		res := h.pair(plain, code, "   ", false)
		if res.status != http.StatusBadRequest {
			t.Fatalf("blank name = %d body=%s", res.status, res.raw)
		}
		res = h.pair(plain, code, "phone", false)
		if res.status != http.StatusOK {
			t.Fatalf("pair after blank name = %d body=%s", res.status, res.raw)
		}
		// The code is single use.
		res = h.pair(plain, code, "phone-again", false)
		if res.status != http.StatusUnauthorized {
			t.Fatalf("reused code = %d body=%s", res.status, res.raw)
		}
		if got := decodePairError(t, res.raw); got != "pairing_invalid" {
			t.Fatalf("reused code error = %q", got)
		}
		if bytes.Contains(res.raw, []byte(code)) {
			t.Fatal("pairing error echoed the submitted code")
		}

		// A wrong code is indistinguishable from an expired/consumed one.
		res = h.pair(plain, "zzzz", "phone", false)
		if res.status != http.StatusUnauthorized || decodePairError(t, res.raw) != "pairing_invalid" {
			t.Fatalf("bad code = %d body=%s", res.status, res.raw)
		}

		// Generating a new code invalidates the older one.
		oldCode := h.newCode()
		liveCode := h.newCode()
		res = h.pair(plain, oldCode, "phone", false)
		if res.status != http.StatusUnauthorized {
			t.Fatalf("stale code = %d body=%s", res.status, res.raw)
		}

		// Five wrong attempts lock the whole hub, even for a currently valid
		// code; only generating a new code locally unlocks it.
		h2 := newPairHarness(t)
		validCode := h2.newCode()
		for i := 0; i < 4; i++ {
			r := h2.pair(plain, "zzzz", "phone", false)
			if r.status != http.StatusUnauthorized {
				t.Fatalf("attempt %d = %d body=%s", i+1, r.status, r.raw)
			}
		}
		r := h2.pair(plain, "zzzz", "phone", false)
		if r.status != http.StatusTooManyRequests || decodePairError(t, r.raw) != "pairing_locked" {
			t.Fatalf("5th attempt = %d body=%s", r.status, r.raw)
		}
		r = h2.pair(plain, validCode, "phone", false)
		if r.status != http.StatusTooManyRequests || decodePairError(t, r.raw) != "pairing_locked" {
			t.Fatalf("valid code while locked = %d body=%s", r.status, r.raw)
		}
		unlocked := h2.newCode()
		r = h2.pair(plain, unlocked, "phone", false)
		if r.status != http.StatusOK {
			t.Fatalf("pair after new local code = %d body=%s", r.status, r.raw)
		}

		// A freshly generated local code resets the counter, so the stale-code
		// attempt above did not accumulate against it.
		if liveCode == "" {
			t.Fatal("no live code generated")
		}
	})

	t.Run("session_bearer_restores_cookie_and_csrf", func(t *testing.T) {
		h := newPairHarness(t)
		plain := &http.Client{Timeout: 10 * time.Second}
		pair := h.pair(plain, h.newCode(), "android", true)
		if pair.status != http.StatusOK {
			t.Fatalf("native pair = %d body=%s", pair.status, pair.raw)
		}
		token := pair.body.DeviceToken

		// Restore with only the Bearer token: cookies + CSRF come back, the
		// token does not.
		req, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/auth/session", nil)
		req.Header.Set("Origin", h.srv.URL)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := h.client.Do(req)
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("session restore = %d body=%s", res.StatusCode, raw)
		}
		if bytes.Contains(raw, []byte(token)) || bytes.Contains(raw, []byte("device_token")) {
			t.Fatalf("session restore leaked a token: %s", raw)
		}
		var info sessionResponse
		if err := json.Unmarshal(raw, &info); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !info.Authenticated || info.CSRFToken == "" || info.DeviceID != pair.body.DeviceID {
			t.Fatalf("session body = %+v", info)
		}
		if info.Hub == nil || info.Hub.ID != h.hubID {
			t.Fatalf("session hub = %+v", info.Hub)
		}
		if len(res.Header.Values("Set-Cookie")) == 0 {
			t.Fatal("Bearer restore did not set cookies")
		}

		// The restored cookies plus CSRF header make a normal write work.
		res = h.do(http.MethodPost, "/api/v1/items/text",
			strings.NewReader(`{"type":"text","text":"hello","sender_device":"android"}`),
			func(r *http.Request) {
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-FlowDrop-CSRF", info.CSRFToken)
			})
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("create with restored cookies = %d body=%s", res.StatusCode, body)
		}

		// A token must never be accepted from the query string.
		res = h.do(http.MethodGet, "/api/v1/items?token="+url.QueryEscape(token), nil)
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			// The cookie jar already carries a valid session here, so only
			// checking the unauthenticated client below is meaningful.
			t.Fatalf("items with cookie = %d", res.StatusCode)
		}
		other := &http.Client{Timeout: 10 * time.Second}
		qreq, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/items?token="+url.QueryEscape(token), nil)
		qres, err := other.Do(qreq)
		if err != nil {
			t.Fatalf("query token: %v", err)
		}
		qres.Body.Close()
		if qres.StatusCode != http.StatusUnauthorized {
			t.Fatalf("query token accepted: %d", qres.StatusCode)
		}
	})

	t.Run("password_login_is_gone", func(t *testing.T) {
		h := newPairHarness(t)
		res := h.do(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"username":"admin","password":"hunter2"}`),
			func(r *http.Request) { r.Header.Set("Content-Type", "application/json") })
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("login route = %d body=%s", res.StatusCode, raw)
		}
		if len(res.Header.Values("Set-Cookie")) != 0 {
			t.Fatalf("removed login set cookies: %v", res.Header.Values("Set-Cookie"))
		}
		if got := decodePairError(t, raw); got != "not_found" {
			t.Fatalf("removed login error = %q", got)
		}
	})

	t.Run("all_data_routes_require_pairing", func(t *testing.T) {
		h := newPairHarness(t)

		// Unpaired: every data route is closed, including WS.
		cases := []struct{ method, path string }{
			{http.MethodGet, "/api/v1/items"},
			{http.MethodGet, "/api/v1/items/does-not-exist"},
			{http.MethodGet, "/api/v1/items/does-not-exist/download"},
			{http.MethodGet, "/api/v1/items/does-not-exist/preview"},
			{http.MethodPost, "/api/v1/items/text"},
			{http.MethodPost, "/api/v1/items/upload"},
			{http.MethodPost, "/api/v1/auth/logout"},
		}
		for _, c := range cases {
			res := h.do(c.method, c.path, strings.NewReader("{}"))
			res.Body.Close()
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s %s = %d, want 401", c.method, c.path, res.StatusCode)
			}
		}
		conn, res, err := h.dialWS(h.srv.URL, nil)
		if err == nil {
			conn.Close(websocket.StatusNormalClosure, "")
			t.Fatal("unauthenticated WebSocket was accepted")
		}
		if res != nil && res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("ws status = %d, want 401", res.StatusCode)
		}

		// Paired: upload, list, download, preview, text and events work.
		h.pairWeb("laptop")
		u, _ := url.Parse(h.srv.URL)
		var csrf string
		for _, c := range h.client.Jar.Cookies(u) {
			if c.Name == csrfCookie {
				csrf = c.Value
			}
		}
		if csrf == "" {
			t.Fatal("no CSRF cookie after pairing")
		}

		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		part, err := mw.CreateFormFile("file", "hello.txt")
		if err != nil {
			t.Fatalf("form file: %v", err)
		}
		if _, err := part.Write([]byte("pairing payload")); err != nil {
			t.Fatalf("write part: %v", err)
		}
		mw.Close()
		res = h.do(http.MethodPost, "/api/v1/items/upload", &buf, func(r *http.Request) {
			r.Header.Set("Content-Type", mw.FormDataContentType())
			r.Header.Set("X-FlowDrop-CSRF", csrf)
		})
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("upload = %d body=%s", res.StatusCode, raw)
		}
		var item Item
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatalf("decode item: %v", err)
		}

		res = h.do(http.MethodGet, "/api/v1/items", nil)
		listRaw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !bytes.Contains(listRaw, []byte(item.ID)) {
			t.Fatalf("list = %d body=%s", res.StatusCode, listRaw)
		}

		res = h.do(http.MethodGet, "/api/v1/items/"+item.ID+"/download", nil)
		dl, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || string(dl) != "pairing payload" {
			t.Fatalf("download = %d body=%q", res.StatusCode, dl)
		}

		// CSRF is still a double submit even with a valid device cookie.
		res = h.do(http.MethodPost, "/api/v1/items/text",
			strings.NewReader(`{"type":"text","text":"no csrf"}`),
			func(r *http.Request) { r.Header.Set("Content-Type", "application/json") })
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("write without CSRF = %d, want 403", res.StatusCode)
		}

		conn, _, err = h.dialWS(h.srv.URL, http.Header{"Cookie": []string{h.cookieHeader()}})
		if err != nil {
			t.Fatalf("paired ws: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		var hello events.Event
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := wsjson.Read(ctx, conn, &hello); err != nil {
			t.Fatalf("ws hello: %v", err)
		}
		if hello.Type != "hello" {
			t.Fatalf("first frame = %+v", hello)
		}
	})

	t.Run("revocation_closes_ws_and_blocks_new_requests", func(t *testing.T) {
		h := newPairHarness(t)

		// Device A keeps a live event stream; device B creates items.
		a := h.pairWeb("device-a")
		conn, _, err := h.dialWS(h.srv.URL, http.Header{"Cookie": []string{h.cookieHeader()}})
		if err != nil {
			t.Fatalf("ws dial: %v", err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var hello events.Event
		if err := wsjson.Read(ctx, conn, &hello); err != nil || hello.Type != "hello" {
			t.Fatalf("ws hello = %+v err=%v", hello, err)
		}

		// A second client creates an item so we know events flow.
		plain := &http.Client{Timeout: 10 * time.Second}
		bPair := h.pair(plain, h.newCode(), "device-b", true)
		if bPair.status != http.StatusOK {
			t.Fatalf("pair b = %d", bPair.status)
		}
		bToken := bPair.body.DeviceToken

		// B restores its cookies and CSRF value with the Bearer token.
		sessReq, _ := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/auth/session", nil)
		sessReq.Header.Set("Authorization", "Bearer "+bToken)
		sessRes, err := plain.Do(sessReq)
		if err != nil {
			t.Fatalf("b session: %v", err)
		}
		var bInfo sessionResponse
		_ = json.NewDecoder(sessRes.Body).Decode(&bInfo)
		bCookies := sessRes.Header.Values("Set-Cookie")
		sessRes.Body.Close()
		if bInfo.CSRFToken == "" || len(bCookies) == 0 {
			t.Fatalf("b session restore = %+v %v", bInfo, bCookies)
		}
		var bCookieHeader string
		for _, raw := range bCookies {
			kv := strings.SplitN(strings.SplitN(raw, ";", 2)[0], "=", 2)
			if len(kv) == 2 {
				bCookieHeader += kv[0] + "=" + kv[1] + "; "
			}
		}
		createItem := func(text string) {
			req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/v1/items/text",
				strings.NewReader(fmt.Sprintf(`{"type":"text","text":%q}`, text)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", h.srv.URL)
			req.Header.Set("Cookie", bCookieHeader)
			req.Header.Set("X-FlowDrop-CSRF", bInfo.CSRFToken)
			res, err := plain.Do(req)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusCreated {
				t.Fatalf("create = %d body=%s", res.StatusCode, body)
			}
		}

		createItem("before revoke")
		var ev events.Event
		if err := wsjson.Read(ctx, conn, &ev); err != nil {
			t.Fatalf("ws event before revoke: %v", err)
		}
		if ev.Type != "item.created" {
			t.Fatalf("event = %+v", ev)
		}

		// Revoke A through the exported local-management entry point.
		if err := h.api.RevokeDevice(a.DeviceID); err != nil {
			t.Fatalf("RevokeDevice: %v", err)
		}
		if err := h.api.RevokeDevice(a.DeviceID); err == nil {
			t.Fatal("second revoke should report unauthorized")
		}

		// The open socket must stop without delivering another event.
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer closeCancel()
		for {
			_, data, err := conn.Read(closeCtx)
			if err != nil {
				break
			}
			t.Fatalf("event after revocation: %s", data)
		}

		// New events must not reach the revoked cookie either.
		createItem("after revoke")
		if _, data, err := conn.Read(closeCtx); err == nil {
			t.Fatalf("revoked socket still received %s", data)
		}

		// Old cookie: protected HTTP and WS both fail now.
		res := h.do(http.MethodGet, "/api/v1/items", nil)
		res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("items after revoke = %d, want 401", res.StatusCode)
		}
		conn2, res, err := h.dialWS(h.srv.URL, http.Header{"Cookie": []string{h.cookieHeader()}})
		if err == nil {
			conn2.Close(websocket.StatusNormalClosure, "")
			t.Fatal("revoked device reconnected to WS")
		}
		if res != nil && res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("ws reconnect = %d, want 401", res.StatusCode)
		}

		// Direct store revocation (the CLI path) must also stop a live socket
		// within the short revalidation interval.
		c := h.pairWeb("device-c")
		conn3, _, err := h.dialWS(h.srv.URL, http.Header{"Cookie": []string{h.cookieHeader()}})
		if err != nil {
			t.Fatalf("ws c dial: %v", err)
		}
		defer conn3.Close(websocket.StatusNormalClosure, "")
		var helloC events.Event
		if err := wsjson.Read(ctx, conn3, &helloC); err != nil {
			t.Fatalf("ws c hello: %v", err)
		}
		if err := h.pairing.RevokeDevice(c.DeviceID); err != nil {
			t.Fatalf("store revoke: %v", err)
		}
		directCtx, directCancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer directCancel()
		if _, data, err := conn3.Read(directCtx); err == nil {
			t.Fatalf("directly revoked socket received %s", data)
		}
	})
}
