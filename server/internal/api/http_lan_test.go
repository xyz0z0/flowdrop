package api

// These tests pin the native-client HTTP/LAN contract: the Hub is configured
// for plain HTTP on a trusted LAN (non-loopback Host, cookie_secure=false) and
// every request arrives with a non-loopback Host and a matching Origin.
//
// The TCP listener is local so the suite stays hermetic, but the client uses a
// LAN hostname in the URL (and therefore in Host/Origin) and dials the loopback
// listener through a custom transport. From the Hub's point of view this is an
// ordinary HTTP request from 192.168.x.x-like LAN traffic.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
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

// lanTestHost stands in for the LAN IP / mDNS name a phone would use.
const lanTestHost = "flowdrop.lan"

type lanHarness struct {
	t          *testing.T
	baseURL    string // http://flowdrop.lan:<port>
	dialAddr   string // 127.0.0.1:<port>
	pairing    *pairing.Store
	client     *http.Client
	csrf       string
	cookies    []*http.Cookie
	setCookies []string
}

// newLANHarness starts a Hub configured exactly like deploy/http-lan.env.example:
// explicit non-loopback listen, explicit allowed_hosts, cookie_secure=false.
func newLANHarness(t *testing.T, mutate ...func(*config.Config)) *lanHarness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Listen = "192.168.1.50:8080" // explicit LAN address, never 0.0.0.0
	cfg.CookieSecure = false         // plaintext HTTP on a trusted LAN
	cfg.AllowedHosts = []string{lanTestHost}
	cfg.MaxUploadBytes = 1 << 20
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
	pstore := pairing.NewStore(conn)

	apiSrv := New(Deps{
		Config:  cfg,
		Pairing: pstore,
		Store:   store.New(conn),
		Storage: files,
		Broker:  events.NewBroker(),
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ts := httptest.NewServer(apiSrv.Handler())
	t.Cleanup(ts.Close)

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test URL: %v", err)
	}
	h := &lanHarness{
		t:        t,
		baseURL:  "http://" + lanTestHost + ":" + u.Port(),
		dialAddr: u.Host,
		pairing:  pstore,
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	h.client = &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, h.dialAddr)
			},
		},
		Timeout: 30 * time.Second,
	}
	return h
}

func (h *lanHarness) do(method, path string, body io.Reader, mutate ...func(*http.Request)) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(method, h.baseURL+path, body)
	if err != nil {
		h.t.Fatalf("request: %v", err)
	}
	// Native clients (and the SPA) send the Origin that matches the Hub URL.
	req.Header.Set("Origin", h.baseURL)
	for _, m := range mutate {
		m(req)
	}
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

func (h *lanHarness) postJSON(path, body string) *http.Response {
	return h.do(http.MethodPost, path, strings.NewReader(body), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
}

// pair generates a local code and registers this client over plaintext LAN,
// exactly like a native client's first-run flow.
func (h *lanHarness) pair() {
	h.t.Helper()
	code, err := h.pairing.CreateCode(time.Now())
	if err != nil {
		h.t.Fatalf("create pairing code: %v", err)
	}
	body := fmt.Sprintf(`{"code":%q,"device_name":"lan-client"}`, code.Code)
	req, _ := http.NewRequest(http.MethodPost, h.baseURL+"/api/v1/auth/pair", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", h.baseURL)
	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("pair: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		h.t.Fatalf("pair = %d body=%s", res.StatusCode, b)
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
	h.cookies = res.Cookies()
	h.setCookies = res.Header.Values("Set-Cookie")
}

func (h *lanHarness) upload(name string, content []byte, sender string) (*http.Response, []byte) {
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
		h.t.Fatalf("close multipart: %v", err)
	}
	res := h.do(http.MethodPost, "/api/v1/items/upload", &buf, func(r *http.Request) {
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func (h *lanHarness) cookie(name string) *http.Cookie {
	h.t.Helper()
	for _, c := range h.cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func readErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode error body: %v body=%s", err, body)
	}
	return out.Error.Code
}

// TestHTTPLANNativeClientContract walks the frozen native-client flow over a
// plaintext, non-loopback Host: pairing, session restore, text/upload/download,
// and logout. It also pins the cookie attributes HTTP mode depends on.
func TestHTTPLANNativeClientContract(t *testing.T) {
	h := newLANHarness(t)

	// Health is reachable with the LAN Host and needs no auth.
	res := h.do(http.MethodGet, "/api/v1/health", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health over LAN Host = %d", res.StatusCode)
	}
	if hsts := res.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Fatalf("HSTS sent on plaintext HTTP: %q", hsts)
	}

	// Anonymous access stays rejected over HTTP/LAN.
	res = h.do(http.MethodGet, "/api/v1/items", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous items = %d, want 401", res.StatusCode)
	}
	res = h.do(http.MethodPost, "/api/v1/items/text", strings.NewReader(`{"text":"nope"}`), func(r *http.Request) {
		r.Header.Set("Content-Type", "application/json")
	})
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous write = %d, want 401", res.StatusCode)
	}

	h.pair()

	// Cookies must be usable over plaintext HTTP: no Secure attribute, but
	// session cookie stays HttpOnly and both stay SameSite=Strict.
	session := h.cookie(sessionCookie)
	if session == nil {
		t.Fatalf("no %s cookie in pairing response: %v", sessionCookie, h.setCookies)
	}
	if session.Secure {
		t.Fatal("session cookie is Secure over plaintext HTTP; browsers will drop it")
	}
	if !session.HttpOnly {
		t.Fatal("session cookie must stay HttpOnly")
	}
	if session.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie SameSite = %v, want Strict", session.SameSite)
	}
	csrf := h.cookie(csrfCookie)
	if csrf == nil {
		t.Fatalf("no %s cookie in pairing response", csrfCookie)
	}
	if csrf.Secure {
		t.Fatal("CSRF cookie is Secure over plaintext HTTP")
	}
	if csrf.HttpOnly {
		t.Fatal("CSRF cookie must be readable by the client")
	}
	if csrf.Value != h.csrf {
		t.Fatalf("CSRF cookie %q != pairing body token", csrf.Value)
	}

	// Session restore with the same cookie jar returns the same CSRF token.
	res = h.do(http.MethodGet, "/api/v1/auth/session", nil)
	var info sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		res.Body.Close()
		t.Fatalf("decode session: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !info.Authenticated {
		t.Fatalf("session restore = %d authenticated=%v", res.StatusCode, info.Authenticated)
	}
	if info.CSRFToken != h.csrf {
		t.Fatalf("session restore csrf = %q, want %q", info.CSRFToken, h.csrf)
	}

	// Simulate a native app restart that kept the session cookie but lost its
	// in-memory CSRF value/cookie: the Hub must reissue it on GET session.
	u, _ := url.Parse(h.baseURL)
	h.client.Jar.SetCookies(u, []*http.Cookie{{
		Name: csrfCookie, Value: "", Path: "/", MaxAge: -1,
	}})
	res = h.do(http.MethodGet, "/api/v1/auth/session", nil)
	var restored sessionResponse
	if err := json.NewDecoder(res.Body).Decode(&restored); err != nil {
		res.Body.Close()
		t.Fatalf("decode restored session: %v", err)
	}
	reissued := res.Header.Values("Set-Cookie")
	res.Body.Close()
	if !restored.Authenticated {
		t.Fatal("session did not survive losing only the CSRF cookie")
	}
	if restored.CSRFToken == "" || restored.CSRFToken == h.csrf {
		t.Fatalf("CSRF token not reissued: %q", restored.CSRFToken)
	}
	if len(reissued) == 0 {
		t.Fatal("no Set-Cookie when reissuing the CSRF cookie")
	}
	h.csrf = restored.CSRFToken

	// Text write with the double-submit header works over HTTP/LAN.
	res = h.postJSON("/api/v1/items/text", `{"type":"text","text":"hello over LAN","sender_device":"android"}`)
	var created Item
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		res.Body.Close()
		t.Fatalf("decode created item: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated || created.ID == "" {
		t.Fatalf("create text = %d item=%+v", res.StatusCode, created)
	}

	// Upload then download the exact bytes.
	res, body := h.upload("lan.txt", []byte("plaintext LAN attachment"), "android")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d body=%s", res.StatusCode, body)
	}
	var uploaded Item
	if err := json.Unmarshal(body, &uploaded); err != nil {
		t.Fatalf("decode uploaded item: %v body=%s", err, body)
	}
	if uploaded.Blob == nil {
		t.Fatalf("uploaded item has no blob: %+v", uploaded)
	}

	res = h.do(http.MethodGet, "/api/v1/items/"+uploaded.ID+"/download", nil)
	downloaded, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download = %d body=%s", res.StatusCode, downloaded)
	}
	if !bytes.Equal(downloaded, []byte("plaintext LAN attachment")) {
		t.Fatalf("download bytes = %q", downloaded)
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("download Content-Disposition = %q", cd)
	}

	// The list endpoint keeps its web/src/api.ts shape.
	res = h.do(http.MethodGet, "/api/v1/items?limit=10", nil)
	var page struct {
		Items []Item `json:"items"`
		Next  string `json:"next_cursor"`
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		res.Body.Close()
		t.Fatalf("decode list: %v", err)
	}
	res.Body.Close()
	if len(page.Items) != 2 {
		t.Fatalf("list returned %d items, want 2", len(page.Items))
	}

	// Logout needs the CSRF header, then the session is gone for every route.
	res = h.do(http.MethodPost, "/api/v1/auth/logout", nil, func(r *http.Request) {
		r.Header.Set("X-FlowDrop-CSRF", h.csrf)
	})
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d, want 204", res.StatusCode)
	}
	res = h.do(http.MethodGet, "/api/v1/items", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("items after logout = %d, want 401", res.StatusCode)
	}
	res = h.do(http.MethodGet, "/api/v1/items/"+uploaded.ID+"/download", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("download after logout = %d, want 401", res.StatusCode)
	}
}

// TestHTTPLANRejectsCrossSiteAndMissingCSRF proves the HTTP/LAN profile does
// not weaken Origin/Host/CSRF checks: only the configured LAN host may drive
// writes, and the double-submit token is still mandatory.
func TestHTTPLANRejectsCrossSiteAndMissingCSRF(t *testing.T) {
	h := newLANHarness(t)
	h.pair()

	textBody := `{"type":"text","text":"must not be created"}`
	withJSON := func(r *http.Request) { r.Header.Set("Content-Type", "application/json") }

	cases := []struct {
		name   string
		origin string
		csrf   string
		want   int
	}{
		{"foreign origin", "http://evil.example", h.csrf, http.StatusForbidden},
		{"origin null", "null", h.csrf, http.StatusForbidden},
		{"origin on another allowed loopback name", "http://127.0.0.1:9", h.csrf, http.StatusForbidden},
		{"missing csrf", "", "", http.StatusForbidden},
		{"wrong csrf", "", "not-the-token", http.StatusForbidden},
	}
	for _, tc := range cases {
		res := h.do(http.MethodPost, "/api/v1/items/text", strings.NewReader(textBody),
			func(r *http.Request) {
				withJSON(r)
				if tc.origin != "" {
					r.Header.Set("Origin", tc.origin)
				}
				if tc.csrf != "" {
					r.Header.Set("X-FlowDrop-CSRF", tc.csrf)
				}
			})
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != tc.want {
			t.Errorf("%s = %d, want %d (body=%s)", tc.name, res.StatusCode, tc.want, body)
		}
		if tc.csrf == "" || tc.csrf != h.csrf {
			if code := readErrorCode(t, body); code != "csrf_missing" && code != "csrf_invalid" {
				t.Errorf("%s error code = %q", tc.name, code)
			}
		}
	}

	// A Host outside allowed_hosts is still rejected with 421, even on LAN.
	res := h.do(http.MethodGet, "/api/v1/health", nil, func(r *http.Request) {
		r.Host = "attacker.example:8080"
	})
	res.Body.Close()
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("unknown Host = %d, want 421", res.StatusCode)
	}

	// None of the rejected writes may have been persisted.
	res = h.do(http.MethodGet, "/api/v1/items", nil)
	var page struct {
		Items []Item `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&page); err != nil {
		res.Body.Close()
		t.Fatalf("decode list: %v", err)
	}
	res.Body.Close()
	if len(page.Items) != 0 {
		t.Fatalf("%d items created by rejected requests", len(page.Items))
	}
}

// TestHTTPLANWebSocketContract covers WS over plaintext LAN: the handshake uses
// the same Cookie and a matching Origin, hello/broadcast frames arrive, and
// foreign-origin or anonymous handshakes are still refused.
func TestHTTPLANWebSocketContract(t *testing.T) {
	h := newLANHarness(t)
	h.pair()

	wsURL := "ws" + strings.TrimPrefix(h.baseURL, "http") + "/api/v1/events"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: h.client,
		HTTPHeader: http.Header{"Origin": []string{h.baseURL}},
	})
	if err != nil {
		t.Fatalf("WS handshake over HTTP/LAN: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	var hello events.Event
	if err := wsjson.Read(ctx, conn, &hello); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if hello.Type != "hello" {
		t.Fatalf("first frame = %+v, want hello", hello)
	}

	// A write over HTTP must reach the same-Cookie WS client.
	res := h.postJSON("/api/v1/items/text", `{"type":"text","text":"broadcast over LAN","sender_device":"kubuntu"}`)
	var item Item
	if err := json.NewDecoder(res.Body).Decode(&item); err != nil {
		res.Body.Close()
		t.Fatalf("decode item: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create text = %d", res.StatusCode)
	}
	var ev events.Event
	if err := wsjson.Read(ctx, conn, &ev); err != nil {
		t.Fatalf("read item.created: %v", err)
	}
	if ev.Type != "item.created" || ev.ID != item.ID {
		t.Fatalf("event = %+v, want item.created %s", ev, item.ID)
	}

	// Foreign Origin is refused even with a valid session cookie.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	badConn, badRes, err := websocket.Dial(ctx2, wsURL, &websocket.DialOptions{
		HTTPClient: h.client,
		HTTPHeader: http.Header{"Origin": []string{"http://evil.example"}},
	})
	if err == nil {
		badConn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("foreign-origin WS was accepted over HTTP/LAN")
	}
	if badRes == nil || badRes.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign-origin WS status = %v, want 403", badRes)
	}

	// The frozen native contract is that Origin carries the Hub's exact
	// scheme://host:port. A same-name Origin with the wrong port is refused by
	// the WS layer, so Android must build Origin from the URL it dialed.
	ctxPort, cancelPort := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPort()
	portConn, portRes, err := websocket.Dial(ctxPort, wsURL, &websocket.DialOptions{
		HTTPClient: h.client,
		HTTPHeader: http.Header{"Origin": []string{"http://" + lanTestHost + ":1"}},
	})
	if err == nil {
		portConn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("WS accepted an Origin whose port does not match the Hub URL")
	}
	if portRes == nil || portRes.StatusCode != http.StatusForbidden {
		t.Fatalf("port-mismatched Origin WS status = %v, want 403", portRes)
	}

	// Anonymous handshake (no cookie jar) is refused with 401.
	anonTransport := h.client.Transport
	anon := &http.Client{Transport: anonTransport, Timeout: 5 * time.Second}
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	anonConn, anonRes, err := websocket.Dial(ctx3, wsURL, &websocket.DialOptions{
		HTTPClient: anon,
		HTTPHeader: http.Header{"Origin": []string{h.baseURL}},
	})
	if err == nil {
		anonConn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("anonymous WS was accepted over HTTP/LAN")
	}
	if anonRes == nil || anonRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous WS status = %v, want 401", anonRes)
	}
}

// TestHTTPLANListenIPIsAnAllowedHost pins the Host allowlist convenience that
// makes "bind the LAN IP, browse http://<lan-ip>:8080" work without also
// duplicating that IP in allowed_hosts. Wildcard binds deliberately do not get
// this treatment, so exposure on every interface still requires explicit Hosts.
func TestHTTPLANListenIPIsAnAllowedHost(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = "192.168.1.50:8080" // exactly the configured LAN address
	hosts := buildAllowedHosts(cfg)
	for _, want := range []string{"192.168.1.50", "localhost", "127.0.0.1", "::1"} {
		if _, ok := hosts[want]; !ok {
			t.Errorf("Host %q missing from allowlist %v", want, hosts)
		}
	}
	if _, ok := hosts["192.168.1.51"]; ok {
		t.Error("a LAN IP that was not configured became an allowed Host")
	}

	for _, addr := range []string{"0.0.0.0:8080", ":8080", "[::]:8080"} {
		c := config.Default()
		c.Listen = addr
		for _, forbidden := range []string{"0.0.0.0", "::"} {
			if _, ok := buildAllowedHosts(c)[forbidden]; ok {
				t.Errorf("wildcard listen %q added Host %q", addr, forbidden)
			}
		}
	}
}

// TestHTTPLANWithSecureCookiesIsUnusable documents the failure mode behind the
// "logged in but immediately logged out" report: HTTPS-only cookies cannot be
// kept over plaintext HTTP. The Hub stays secure by default; switching modes
// requires cookie_secure=false, it is never inferred from the Host header.
func TestHTTPLANWithSecureCookiesIsUnusable(t *testing.T) {
	h := newLANHarness(t, func(c *config.Config) { c.CookieSecure = true })
	h.pair()

	session := h.cookie(sessionCookie)
	if session == nil {
		t.Fatalf("no %s cookie: %v", sessionCookie, h.setCookies)
	}
	if !session.Secure {
		t.Fatal("cookie_secure=true must mark the session cookie Secure even over HTTP")
	}
	if csrf := h.cookie(csrfCookie); csrf == nil || !csrf.Secure {
		t.Fatal("cookie_secure=true must mark the CSRF cookie Secure too")
	}

	// HSTS must not be sent for a plaintext response, even in secure-cookie mode.
	res := h.do(http.MethodGet, "/api/v1/health", nil)
	res.Body.Close()
	if hsts := res.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Fatalf("HSTS on a plaintext response: %q", hsts)
	}
}

// TestAdditionalListenIPsAreAllowedHosts pins the multi-listener Host
// allowlist: the primary and every explicit additional IP are accepted, while
// an unconfigured IP and wildcard binds are not.
func TestAdditionalListenIPsAreAllowedHosts(t *testing.T) {
	cfg := config.Default()
	cfg.Listen = "192.168.1.50:8080"
	cfg.AdditionalListen = []string{"100.64.0.7:8080", "192.168.1.51:8081"}
	hosts := buildAllowedHosts(cfg)
	for _, want := range []string{"192.168.1.50", "100.64.0.7", "192.168.1.51", "localhost", "127.0.0.1", "::1"} {
		if _, ok := hosts[want]; !ok {
			t.Errorf("Host %q missing from allowlist %v", want, hosts)
		}
	}
	if _, ok := hosts["192.168.1.52"]; ok {
		t.Error("an IP that was not configured became an allowed Host")
	}
}
