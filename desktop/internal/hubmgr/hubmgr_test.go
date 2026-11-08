package hubmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"flowdrop/pkg/hub"
)

// clearFlowdropEnv removes every FLOWDROP_* variable for the duration of the
// test so the Hub configuration is fully deterministic.
func clearFlowdropEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "FLOWDROP_") {
			old, had := os.LookupEnv(key)
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
			keyCopy := key
			t.Cleanup(func() {
				if had {
					os.Setenv(keyCopy, old)
				}
			})
		}
	}
}

func newTestManager(t *testing.T) (*Manager, Config) {
	t.Helper()
	clearFlowdropEnv(t)
	dir := t.TempDir()
	cfg := Config{
		ConfigPath:  filepath.Join(dir, "config.json"),
		DataDir:     filepath.Join(dir, "data"),
		BindAddress: "127.0.0.1",
		Port:        freePort(t),
	}
	return New(), cfg
}

// freePort reserves an ephemeral loopback port and returns it. The window
// between closing the probe listener and the Hub binding it is tiny and only
// affects test flakiness, never production behaviour.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startEphemeral picks a free loopback port and starts the Hub there. Using an
// ephemeral port keeps the test independent of whatever runs on this machine.
func startEphemeral(t *testing.T, m *Manager, cfg Config) string {
	t.Helper()
	if err := m.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := m.Stop(c); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})
	base := m.URL()
	if base == "" {
		t.Fatal("URL() is empty after a successful Start")
	}
	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("URL() = %q, want loopback base URL", base)
	}
	return base
}

// httpClient returns a browser-like client with a cookie jar and an Origin
// header matching the Hub, mirroring what the desktop WebView sends.
func httpClient(t *testing.T, base string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{
		Jar:     jar,
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func doJSON(t *testing.T, c *http.Client, base, method, path string, body any, csrf string) (*http.Response, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", base)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-FlowDrop-CSRF", csrf)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	var decoded map[string]any
	raw, _ := io.ReadAll(res.Body)
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &decoded)
	}
	return res, decoded
}

// TestLifecycleAndFirstAdmin covers the frozen Hub interface as the desktop
// shell uses it: New/Start, HasAdmin, InitializeAdmin (first only), Stop,
// re-Start on a different port against the same data directory.
func TestLifecycleAndFirstAdmin(t *testing.T) {
	m, cfg := newTestManager(t)
	base := startEphemeral(t, m, cfg)

	ok, err := m.HasAdmin()
	if err != nil {
		t.Fatalf("HasAdmin: %v", err)
	}
	if ok {
		t.Fatal("HasAdmin = true on a fresh data directory")
	}

	if err := m.InitializeAdmin("admin", "short"); err == nil {
		t.Fatal("InitializeAdmin accepted a short password")
	}
	if err := m.InitializeAdmin("admin", "correct-horse-battery"); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	if ok, err = m.HasAdmin(); err != nil || !ok {
		t.Fatalf("HasAdmin after init = %v,%v", ok, err)
	}
	// Initializing twice must never overwrite the existing password.
	err = m.InitializeAdmin("admin", "another-password-123")
	if !errors.Is(err, hub.ErrAdminExists) {
		t.Fatalf("second InitializeAdmin = %v, want hub.ErrAdminExists", err)
	}

	// The previous password still works.
	client := httpClient(t, base)
	res, decoded := doJSON(t, client, base, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-battery"}, "")
	if res.StatusCode != http.StatusOK || decoded["authenticated"] != true {
		t.Fatalf("login after failed overwrite = %d %v", res.StatusCode, decoded)
	}
	// And the rejected password does not.
	res2, _ := doJSON(t, httpClient(t, base), base, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "another-password-123"}, "")
	if res2.StatusCode == http.StatusOK {
		t.Fatal("the rejected password was accepted")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if m.URL() != "" {
		t.Fatalf("URL() = %q after Stop, want empty", m.URL())
	}
	if _, err := m.HasAdmin(); err == nil {
		t.Fatal("HasAdmin succeeded after Stop, want ErrClosed-style error")
	}
}

// TestStopThenStartReusesManager proves a Manager can be stopped and started
// again (the settings window's rollback path relies on it).
func TestStopThenStartReusesManager(t *testing.T) {
	m, cfg := newTestManager(t)
	startEphemeral(t, m, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	cfg.Port = freePort(t)
	if err := m.Start(cfg); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	if m.URL() == "" {
		t.Fatal("URL() is empty after a Stop/Start cycle")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("final Stop: %v", err)
	}
}

// TestAddressSwitchKeepsData verifies the "stop old Hub, start a new one on
// another address with the same data directory" flow the settings window uses.
func TestAddressSwitchKeepsData(t *testing.T) {
	m, cfg := newTestManager(t)
	base := startEphemeral(t, m, cfg)

	if err := m.InitializeAdmin("admin", "correct-horse-battery"); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	c := httpClient(t, base)
	res, sess := doJSON(t, c, base, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-battery"}, "")
	csrf, _ := sess["csrf_token"].(string)
	if res.StatusCode != http.StatusOK || csrf == "" {
		t.Fatalf("login = %d %v", res.StatusCode, sess)
	}
	res, item := doJSON(t, c, base, http.MethodPost, "/api/v1/items/text",
		map[string]string{"text": "switch me"}, csrf)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create text = %d %v", res.StatusCode, item)
	}

	next := cfg
	next.Port = freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Restart(ctx, next); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	newBase := m.URL()
	if newBase == "" || newBase == base {
		t.Fatalf("Restart URL() = %q (was %q), want a new bound URL", newBase, base)
	}

	// The admin password and the item survive the switch (same data dir).
	c2 := httpClient(t, newBase)
	res, sess = doJSON(t, c2, newBase, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-battery"}, "")
	csrf, _ = sess["csrf_token"].(string)
	if res.StatusCode != http.StatusOK || csrf == "" {
		t.Fatalf("login after switch = %d %v", res.StatusCode, sess)
	}
	res, page := doJSON(t, c2, newBase, http.MethodGet, "/api/v1/items", nil, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list after switch = %d %v", res.StatusCode, page)
	}
	items, _ := page["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items after switch = %d, want 1: %v", len(items), page)
	}
}

// TestRestartReleasesOldPort proves the old listener is gone after a switch.
func TestRestartReleasesOldPort(t *testing.T) {
	m, cfg := newTestManager(t)
	base := startEphemeral(t, m, cfg)
	oldURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}

	next := cfg
	next.Port = freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := m.Restart(ctx, next); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(oldURL.String() + "/api/v1/health")
	if err == nil {
		res.Body.Close()
		t.Fatalf("old listener at %s is still accepting connections", oldURL)
	}
}

// TestStartPortConflictIsFriendly verifies the shell can show an actionable
// message (and never kills the other process).
func TestStartPortConflictIsFriendly(t *testing.T) {
	m, cfg := newTestManager(t)
	base := startEphemeral(t, m, cfg)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	port := 0
	fmt.Sscanf(u.Port(), "%d", &port)

	// A second Hub on the same address must fail, and the message must tell the
	// user to change the port instead of implying a killed process.
	other := New()
	otherCfg := cfg
	otherCfg.DataDir = filepath.Join(t.TempDir(), "data2")
	otherCfg.Port = port
	err = other.Start(otherCfg)
	if err == nil {
		other.Stop(context.Background())
		t.Fatal("second Hub bound the same port, want failure")
	}
	msg := FriendlyError(err)
	if !strings.Contains(msg, "端口已被占用") || !strings.Contains(msg, "不会结束") {
		t.Fatalf("FriendlyError(port conflict) = %q", msg)
	}
}

// TestDataDirLockIsFriendly verifies a second Hub on the same data directory is
// refused with a message that does not suggest killing anything.
func TestDataDirLockIsFriendly(t *testing.T) {
	m, cfg := newTestManager(t)
	_ = startEphemeral(t, m, cfg)

	other := New()
	otherCfg := cfg
	otherCfg.Port = freePort(t)
	err := other.Start(otherCfg)
	if err == nil {
		other.Stop(context.Background())
		t.Fatal("second Hub started on the same data dir, want failure")
	}
	if !errors.Is(err, hub.ErrDataDirInUse) {
		t.Fatalf("second Start = %v, want hub.ErrDataDirInUse", err)
	}
	msg := FriendlyError(err)
	if !strings.Contains(msg, "数据目录已被另一个 FlowDrop 使用") || !strings.Contains(msg, "不会结束它") {
		t.Fatalf("FriendlyError(data dir) = %q", msg)
	}
}

// TestStatusReportsAdminAndURL keeps the settings window honest about state.
func TestStatusReportsAdminAndURL(t *testing.T) {
	m, cfg := newTestManager(t)
	if st := m.Status(); st.Running || st.URL != "" {
		t.Fatalf("Status before start = %+v", st)
	}
	base := startEphemeral(t, m, cfg)
	st := m.Status()
	if !st.Running || st.URL != base || st.AdminConfigured {
		t.Fatalf("Status after start = %+v, want running URL=%s admin=false", st, base)
	}
	if err := m.InitializeAdmin("admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}
	if st = m.Status(); !st.AdminConfigured {
		t.Fatalf("Status.AdminConfigured = false after InitializeAdmin")
	}
}

// TestDesktopFlowEndToEnd drives the same HTTP surface the WebView uses:
// session cookie + CSRF + text item + upload + preview + authenticated
// download + WebSocket notification.
func TestDesktopFlowEndToEnd(t *testing.T) {
	m, cfg := newTestManager(t)
	base := startEphemeral(t, m, cfg)
	if err := m.InitializeAdmin("admin", "correct-horse-battery"); err != nil {
		t.Fatal(err)
	}

	c := httpClient(t, base)
	res, sess := doJSON(t, c, base, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": "admin", "password": "correct-horse-battery"}, "")
	csrf, _ := sess["csrf_token"].(string)
	if res.StatusCode != http.StatusOK || csrf == "" {
		t.Fatalf("login = %d %v", res.StatusCode, sess)
	}

	// The session cookie must be present and the CSRF token must actually be
	// required (that is what makes the shell's same-origin window correct).
	u, _ := url.Parse(base)
	haveSession := false
	for _, ck := range c.Jar.Cookies(u) {
		if ck.Name == "flowdrop_session" {
			haveSession = true
		}
	}
	if !haveSession {
		t.Fatal("login did not yield a flowdrop_session cookie")
	}
	res, _ = doJSON(t, c, base, http.MethodPost, "/api/v1/items/text", map[string]string{"text": "no csrf"}, "")
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("text without CSRF = %d, want 403", res.StatusCode)
	}

	// WebSocket notification for a new item.
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/api/v1/events"
	header := http.Header{}
	header.Set("Origin", base)
	wsCtx, wsCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer wsCancel()
	conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{
		HTTPClient: c,
		HTTPHeader: header,
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	var hello map[string]string
	if err := wsjson.Read(wsCtx, conn, &hello); err != nil {
		t.Fatalf("websocket hello: %v", err)
	}
	if hello["type"] != "hello" {
		t.Fatalf("websocket first frame = %v, want hello", hello)
	}

	res, item := doJSON(t, c, base, http.MethodPost, "/api/v1/items/text",
		map[string]string{"text": "hello from the desktop shell"}, csrf)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create text = %d %v", res.StatusCode, item)
	}
	var event map[string]string
	if err := wsjson.Read(wsCtx, conn, &event); err != nil {
		t.Fatalf("websocket event: %v", err)
	}
	if event["type"] != "item.created" {
		t.Fatalf("websocket event = %v, want item.created", event)
	}

	// Upload + authenticated download through the same cookie jar.
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "desktop-test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("desktop download body")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/items/upload", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", base)
	req.Header.Set("X-FlowDrop-CSRF", csrf)
	upRes, err := c.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	var uploaded map[string]any
	_ = json.NewDecoder(upRes.Body).Decode(&uploaded)
	upRes.Body.Close()
	if upRes.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d %v", upRes.StatusCode, uploaded)
	}
	id, _ := uploaded["id"].(string)
	if id == "" {
		t.Fatalf("upload response has no id: %v", uploaded)
	}

	dlReq, _ := http.NewRequest(http.MethodGet, base+"/api/v1/items/"+id+"/download", nil)
	dlReq.Header.Set("Origin", base)
	dlRes, err := c.Do(dlReq)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	body, _ := io.ReadAll(dlRes.Body)
	dlRes.Body.Close()
	if dlRes.StatusCode != http.StatusOK {
		t.Fatalf("download = %d %s", dlRes.StatusCode, body)
	}
	if string(body) != "desktop download body" {
		t.Fatalf("download body = %q", body)
	}
	if cd := dlRes.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("download Content-Disposition = %q, want attachment", cd)
	}

	// An unauthenticated client must not be able to download it.
	anon := &http.Client{Timeout: 5 * time.Second}
	anonReq, _ := http.NewRequest(http.MethodGet, base+"/api/v1/items/"+id+"/download", nil)
	anonReq.Header.Set("Origin", base)
	anonRes, err := anon.Do(anonReq)
	if err != nil {
		t.Fatalf("anonymous download: %v", err)
	}
	anonRes.Body.Close()
	if anonRes.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous download = %d, want 401", anonRes.StatusCode)
	}
}

// TestDualListenSharesOneHubAndData binds two loopback addresses (127.0.0.1 and
// 127.0.0.2, both local on Linux) through one Manager. It proves the frozen
// contract: one Hub, one database, one data-dir lock, two real listeners, and
// URLs reporting only addresses that are actually accepting.
func TestDualListenSharesOneHubAndData(t *testing.T) {
	m, cfg := newTestManager(t)
	cfg.AdditionalBindAddresses = []string{"127.0.0.2"}
	cfg.Port = freePortOn(t, "127.0.0.1")
	if err := m.Start(cfg); err != nil {
		t.Fatalf("Start dual: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	})

	urls := m.URLs()
	if len(urls) != 2 {
		t.Fatalf("URLs() = %v, want two bound URLs", urls)
	}
	if !strings.HasPrefix(urls[0], "http://127.0.0.1:") || !strings.HasPrefix(urls[1], "http://127.0.0.2:") {
		t.Fatalf("URLs() = %v, want primary 127.0.0.1 then 127.0.0.2", urls)
	}
	if m.URL() != urls[0] {
		t.Fatalf("URL() = %q, want URLs()[0] = %q", m.URL(), urls[0])
	}
	st := m.Status()
	if len(st.URLs) != 2 || st.URL != urls[0] || len(st.AdditionalAddresses) != 1 || st.AdditionalAddresses[0] != "127.0.0.2" {
		t.Fatalf("Status dual = %+v", st)
	}

	for _, base := range urls {
		res, err := (&http.Client{Timeout: 5 * time.Second}).Get(base + "/api/v1/health")
		if err != nil {
			t.Fatalf("health %s: %v", base, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("health %s = %d", base, res.StatusCode)
		}
	}

	if err := m.InitializeAdmin("admin", "correct-horse-battery"); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}

	// Log in separately on each host: cookies are host-scoped, and this product
	// deliberately does not claim they are shared across hosts. The accounts and
	// items, however, come from the same database.
	login := func(base string) (*http.Client, string) {
		c := httpClient(t, base)
		res, sess := doJSON(t, c, base, http.MethodPost, "/api/v1/auth/login",
			map[string]string{"username": "admin", "password": "correct-horse-battery"}, "")
		csrf, _ := sess["csrf_token"].(string)
		if res.StatusCode != http.StatusOK || csrf == "" {
			t.Fatalf("login %s = %d %v", base, res.StatusCode, sess)
		}
		return c, csrf
	}
	c1, csrf1 := login(urls[0])
	c2, _ := login(urls[1])

	res, item := doJSON(t, c1, urls[0], http.MethodPost, "/api/v1/items/text",
		map[string]string{"text": "written via LAN listener"}, csrf1)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create via primary = %d %v", res.StatusCode, item)
	}

	res, page := doJSON(t, c2, urls[1], http.MethodGet, "/api/v1/items", nil, "")
	var items []any
	if raw, ok := page["items"].([]any); ok {
		items = raw
	}
	if res.StatusCode != http.StatusOK || len(items) != 1 {
		t.Fatalf("list via additional = %d %v, want the item created through the other listener", res.StatusCode, page)
	}

	// Real WebSocket on the additional listener sees the create event from the
	// primary listener: same broker.
	wsURL := "ws" + strings.TrimPrefix(urls[1], "http") + "/api/v1/events"
	header := http.Header{}
	header.Set("Origin", urls[1])
	wsCtx, wsCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer wsCancel()
	conn, _, err := websocket.Dial(wsCtx, wsURL, &websocket.DialOptions{HTTPClient: c2, HTTPHeader: header})
	if err != nil {
		t.Fatalf("websocket on additional listener: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	var hello map[string]string
	if err := wsjson.Read(wsCtx, conn, &hello); err != nil || hello["type"] != "hello" {
		t.Fatalf("websocket hello on additional = %v,%v", hello, err)
	}
	res, second := doJSON(t, c1, urls[0], http.MethodPost, "/api/v1/items/text",
		map[string]string{"text": "broadcast across listeners"}, csrf1)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("second create = %d %v", res.StatusCode, second)
	}
	var event map[string]string
	if err := wsjson.Read(wsCtx, conn, &event); err != nil {
		t.Fatalf("websocket event on additional: %v", err)
	}
	if event["type"] != "item.created" {
		t.Fatalf("websocket event = %v, want item.created", event)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("Stop dual: %v", err)
	}
	if got := m.URLs(); len(got) != 0 {
		t.Fatalf("URLs() after Stop = %v, want none", got)
	}
	for _, base := range urls {
		u, _ := url.Parse(base)
		if _, err := (&http.Client{Timeout: time.Second}).Get(u.String() + "/api/v1/health"); err == nil {
			t.Fatalf("listener %s still accepts connections after Stop", base)
		}
	}
}

// TestDualStartIsAtomic proves a missing second address fails Start without
// leaving the primary listener half-started.
func TestDualStartIsAtomic(t *testing.T) {
	m, cfg := newTestManager(t)
	cfg.Port = freePortOn(t, "127.0.0.1")
	cfg.AdditionalBindAddresses = []string{"192.0.2.123"} // TEST-NET-1, not local
	err := m.Start(cfg)
	if err == nil {
		_ = m.Stop(context.Background())
		t.Fatal("Start with an absent additional address succeeded")
	}
	if m.URL() != "" || len(m.URLs()) != 0 || m.Status().Running {
		t.Fatalf("failed dual Start left a running Hub: %+v", m.Status())
	}
	// The primary port must be free again (the Hub rolled back its bind).
	ln, lerr := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(cfg.Port)))
	if lerr != nil {
		t.Fatalf("primary port %d still bound after failed dual Start: %v", cfg.Port, lerr)
	}
	_ = ln.Close()
}

// TestSingleModeOverridesConfigAdditionalListen proves desktop single mode is
// explicit: an additional_listen left in the Hub config file is not inherited.
func TestSingleModeOverridesConfigAdditionalListen(t *testing.T) {
	m, cfg := newTestManager(t)
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldPort := held.Addr().(*net.TCPAddr).Port
	configJSON := fmt.Sprintf(`{"additional_listen":["127.0.0.1:%d"]}`, heldPort)
	if err := os.WriteFile(cfg.ConfigPath, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(cfg); err != nil {
		t.Fatalf("Start with config additional_listen and desktop single mode: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	if urls := m.URLs(); len(urls) != 1 {
		t.Fatalf("URLs() = %v, want only the primary listener", urls)
	}
}

// freePortOn reserves a free port on a specific local address.
func freePortOn(t *testing.T, host string) int {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("reserve free port on %s: %v", host, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
