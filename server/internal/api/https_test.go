package api

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"

	"flowdrop/internal/auth"
	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/events"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
)

// TestHTTPSReverseProxyContract stands up a real TLS terminator in front of the
// hub, mimicking Tailscale Serve / Caddy: TLS ends at the proxy, the hub sees
// plain HTTP from 127.0.0.1 and must trust X-Forwarded-Proto.
//
// It asserts the deployment contract that matters for security:
//   - cookies are marked Secure over the proxied HTTPS origin
//   - the proxied Host must be in allowed_hosts, otherwise 421
//   - the session still works when X-Forwarded-Proto: https is present
func TestHTTPSReverseProxyContract(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Listen = "127.0.0.1:0"
	cfg.CookieSecure = true // production setting
	cfg.MaxUploadBytes = 1 << 20
	cfg.MinFreeBytes = 0
	cfg.AllowedHosts = []string{"hub.test"}
	cfg.TrustedProxies = []string{"127.0.0.1", "::1"}

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	files, err := storage.New(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	sess := auth.NewStore(conn, cfg.SessionTTL())
	if err := sess.SetPassword("admin", testPassword); err != nil {
		t.Fatal(err)
	}
	_, loopback, _ := net.ParseCIDR("127.0.0.1/32")
	app := New(Deps{
		Config: cfg,

		Sessions:       sess,
		Store:          store.New(conn),
		Storage:        files,
		Broker:         events.NewBroker(),
		Logger:         slog.New(slog.NewTextHandler(io.Discard, nil)),
		TrustedProxies: []*net.IPNet{loopback},
	})
	hub := httptest.NewServer(app.Handler())
	defer hub.Close()

	// Reverse proxy that speaks TLS to the client and rewrites the Host to the
	// public name, exactly like a TLS-terminating proxy would.
	target, _ := url.Parse(hub.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(r *http.Request) {
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		r.Host = "hub.test" // the public hostname the client used
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "hub.test")
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
	}
	tlsSrv := httptest.NewTLSServer(proxy)
	defer tlsSrv.Close()

	// Client that trusts the test CA and keeps cookies.
	pool := x509.NewCertPool()
	pool.AddCert(tlsSrv.Certificate())
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
		Timeout: 10 * time.Second,
	}

	res, err := client.Get(tlsSrv.URL + "/api/v1/health")
	if err != nil {
		t.Fatalf("health over TLS proxy: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health = %d", res.StatusCode)
	}

	body := strings.NewReader(`{"username":"admin","password":"` + testPassword + `"}`)
	req, _ := http.NewRequest(http.MethodPost, tlsSrv.URL+"/api/v1/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	// The browser's Origin is the public name the user typed, which the proxy
	// forwards as Host. The hub requires these to match.
	req.Header.Set("Origin", "https://hub.test")
	res, err = client.Do(req)
	if err != nil {
		t.Fatalf("login over TLS proxy: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login over TLS proxy = %d", res.StatusCode)
	}

	var sessionCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookieNameForTest() {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("no session cookie issued over HTTPS proxy")
	}
	if !sessionCookie.Secure {
		t.Fatal("session cookie must be Secure when the proxy reports https")
	}
	if !sessionCookie.HttpOnly {
		t.Fatal("session cookie must be HttpOnly")
	}

	// HSTS is only emitted when the request is known to be HTTPS.
	res2, err := client.Get(tlsSrv.URL + "/")
	if err != nil {
		t.Fatalf("index over TLS proxy: %v", err)
	}
	res2.Body.Close()
	if hsts := res2.Header.Get("Strict-Transport-Security"); hsts == "" {
		t.Fatal("missing HSTS on an HTTPS request")
	}

	// Authenticated API call through the proxy still works.
	res3, err := client.Get(tlsSrv.URL + "/api/v1/items?limit=1")
	if err != nil {
		t.Fatalf("items over TLS proxy: %v", err)
	}
	defer res3.Body.Close()
	if res3.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res3.Body)
		t.Fatalf("items = %d body=%s", res3.StatusCode, b)
	}
	var page struct {
		Items []store.Item `json:"items"`
	}
	if err := json.NewDecoder(res3.Body).Decode(&page); err != nil {
		t.Fatalf("decode items: %v", err)
	}

	// A Host the proxy does not allow is rejected by the hub (421).
	// Talk to the hub directly to bypass the proxy's Host rewriting.
	hres, err := http.Get(hub.URL + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	hres.Body.Close()
	if hres.StatusCode != http.StatusOK {
		t.Fatalf("direct loopback health = %d", hres.StatusCode)
	}
	reqBad, _ := http.NewRequest(http.MethodGet, hub.URL+"/api/v1/items", nil)
	reqBad.Host = "not-allowed.test"
	res4, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatal(err)
	}
	defer res4.Body.Close()
	if res4.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("disallowed host = %d, want 421", res4.StatusCode)
	}
}

// sessionCookieNameForTest avoids importing the unexported constant name twice.
func sessionCookieNameForTest() string { return sessionCookie }

// TestForwardedProtoIgnoredFromUntrustedPeer proves that a direct client cannot
// claim HTTPS by sending X-Forwarded-Proto itself.
func TestForwardedProtoIgnoredFromUntrustedPeer(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.CookieSecure = false // isolate the effect of the header
		c.TrustedProxies = nil // nothing is trusted
	})
	res := h.do(http.MethodPost, "/api/v1/auth/login",
		strings.NewReader(`{"username":"admin","password":"`+testPassword+`"}`),
		func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", h.srv.URL)
			r.Header.Set("X-Forwarded-Proto", "https")
		})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", res.StatusCode)
	}
	// The request was plain HTTP from an untrusted peer. A spoofed
	// X-Forwarded-Proto must not be believed, so:
	//   - no Secure attribute is derived from it
	//   - HSTS is not emitted for a plaintext response
	for _, c := range res.Cookies() {
		if c.Secure {
			t.Fatalf("cookie %q became Secure from an untrusted X-Forwarded-Proto", c.Name)
		}
	}
	hres := h.do(http.MethodGet, "/", nil, func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
	})
	hres.Body.Close()
	if hsts := hres.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Fatalf("HSTS emitted on a plaintext response: %q", hsts)
	}
}
