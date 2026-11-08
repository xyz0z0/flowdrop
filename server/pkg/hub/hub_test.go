package hub_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"flowdrop/pkg/hub"
)

// testOptions makes every test hermetic: its own data dir and config path, an
// ephemeral loopback port and no mDNS traffic.
func testOptions(t *testing.T, mutate ...func(*hub.Options)) hub.Options {
	t.Helper()
	dir := t.TempDir()
	no := false
	opts := hub.Options{
		ConfigPath:       filepath.Join(dir, "absent-config.json"),
		Listen:           "127.0.0.1:0",
		DataDir:          filepath.Join(dir, "data"),
		CookieSecure:     &no,
		DiscoveryEnabled: &no,
	}
	for _, m := range mutate {
		m(&opts)
	}
	return opts
}

func mustNew(t *testing.T, opts hub.Options) *hub.Hub {
	t.Helper()
	h, err := hub.New(opts)
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	t.Cleanup(func() { stopQuietly(t, h) })
	return h
}

func startHub(t *testing.T, h *hub.Hub) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := h.Start(ctx); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}
	url := h.URL()
	if url == "" {
		t.Fatal("URL() is empty after Start")
	}
	return url
}

func stopHub(t *testing.T, h *hub.Hub) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return h.Stop(ctx)
}

func stopQuietly(t *testing.T, h *hub.Hub) {
	t.Helper()
	if err := stopHub(t, h); err != nil {
		t.Errorf("hub.Stop: %v", err)
	}
}

func doJSON(t *testing.T, method, url, body string, mutate ...func(*http.Request)) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, m := range mutate {
		m(req)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res, b
}

func health(t *testing.T, baseURL string) (int, string) {
	t.Helper()
	res, body := doJSON(t, http.MethodGet, baseURL+"/api/v1/health", "")
	return res.StatusCode, string(body)
}

// pairedSession is what a native pairing returns: the saved device token plus
// the cookies/CSRF of the origin it paired against.
type pairedSession struct {
	Token    string
	CSRF     string
	Cookie   string
	DeviceID string
	HubID    string
}

// pairDevice is the only login path: generate a code locally, consume it over
// HTTP with native=true so the returned device token can be saved and replayed
// later (see restoreSession).
func pairDevice(t *testing.T, h *hub.Hub, baseURL, deviceName string) pairedSession {
	t.Helper()
	code, err := h.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	res, body := doJSON(t, http.MethodPost, baseURL+"/api/v1/auth/pair",
		fmt.Sprintf(`{"code":%q,"device_name":%q,"native":true}`, code.Code, deviceName),
		func(r *http.Request) { r.Header.Set("Origin", baseURL) })
	if res.StatusCode != http.StatusOK {
		t.Fatalf("pair = %d body=%s", res.StatusCode, body)
	}
	var out struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrf_token"`
		DeviceToken   string `json:"device_token"`
		DeviceID      string `json:"device_id"`
		Hub           struct {
			ID string `json:"id"`
		} `json:"hub"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode pair: %v body=%s", err, body)
	}
	if !out.Authenticated || out.CSRFToken == "" || out.DeviceToken == "" || out.DeviceID == "" || out.Hub.ID == "" {
		t.Fatalf("pair response is incomplete: %s", body)
	}
	var pairs []string
	for _, c := range res.Cookies() {
		pairs = append(pairs, c.Name+"="+c.Value)
	}
	if len(pairs) == 0 {
		t.Fatal("pair set no cookies")
	}
	return pairedSession{
		Token:    out.DeviceToken,
		CSRF:     out.CSRFToken,
		Cookie:   strings.Join(pairs, "; "),
		DeviceID: out.DeviceID,
		HubID:    out.Hub.ID,
	}
}

// restoreSession replays a saved device token with GET /auth/session to build
// the cookies/CSRF of one origin. Cookies are host-scoped, so every origin is
// restored separately instead of trying to share one cookie jar.
func restoreSession(t *testing.T, baseURL, token string) (csrf, cookie string) {
	t.Helper()
	res, body := doJSON(t, http.MethodGet, baseURL+"/api/v1/auth/session", "",
		func(r *http.Request) {
			r.Header.Set("Origin", baseURL)
			r.Header.Set("Authorization", "Bearer "+token)
		})
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"authenticated":true`) {
		t.Fatalf("restore session = %d body=%s", res.StatusCode, body)
	}
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode session: %v body=%s", err, body)
	}
	if out.CSRFToken == "" {
		t.Fatalf("restore session returned no csrf token: %s", body)
	}
	var pairs []string
	for _, c := range res.Cookies() {
		pairs = append(pairs, c.Name+"="+c.Value)
	}
	if len(pairs) == 0 {
		t.Fatal("restore session set no cookies")
	}
	return out.CSRFToken, strings.Join(pairs, "; ")
}

// authHeaders attaches a restored origin session to a request.
func authHeaders(baseURL, csrf, cookie string) func(*http.Request) {
	return func(r *http.Request) {
		r.Header.Set("Origin", baseURL)
		r.Header.Set("Cookie", cookie)
		if csrf != "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Header.Set("X-FlowDrop-CSRF", csrf)
		}
	}
}

// TestLifecycleBindServeStop covers the frozen interface end to end: New is
// side-effect free on the network, Start binds synchronously, URL is real, the
// handler keeps auth, Stop releases the port and is idempotent.
func TestLifecycleBindServeStop(t *testing.T) {
	h := mustNew(t, testOptions(t))

	if got := h.URL(); got != "" {
		t.Fatalf("URL() before Start = %q, want empty", got)
	}
	if h.Handler() == nil {
		t.Fatal("Handler() is nil after New")
	}
	// Pairing only exists for a Hub that is actually listening: a configured
	// but unstarted Hub cannot mint a code.
	if _, err := h.CreatePairingCode(); !errors.Is(err, hub.ErrNotStarted) {
		t.Fatalf("CreatePairingCode before Start = %v, want ErrNotStarted", err)
	}

	url := startHub(t, h)
	if !strings.HasPrefix(url, "http://127.0.0.1:") {
		t.Fatalf("URL() = %q, want http://127.0.0.1:<port>", url)
	}
	if _, port, err := net.SplitHostPort(strings.TrimPrefix(url, "http://")); err != nil || port == "0" {
		t.Fatalf("URL() = %q does not carry the real port: %v", url, err)
	}

	status, body := health(t, url)
	if status != http.StatusOK || !strings.Contains(body, "flowdrop-hub") {
		t.Fatalf("health = %d %q, want 200 flowdrop-hub", status, body)
	}

	if err := h.Start(context.Background()); !errors.Is(err, hub.ErrAlreadyStarted) {
		t.Fatalf("second Start err = %v, want ErrAlreadyStarted", err)
	}

	// An unauthenticated client is told plainly that it is not paired yet.
	res, sessBody := doJSON(t, http.MethodGet, url+"/api/v1/auth/session", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(string(sessBody), `"authenticated":false`) {
		t.Fatalf("session before pairing = %d %s, want 200 authenticated=false", res.StatusCode, sessBody)
	}

	// The locally generated code is the only login path.
	sess := pairDevice(t, h, url, "lifecycle-test")
	if sess.HubID == "" {
		t.Fatal("pair response carried no hub id")
	}
	res, sessBody = doJSON(t, http.MethodGet, url+"/api/v1/auth/session", "", authHeaders(url, "", sess.Cookie))
	if res.StatusCode != http.StatusOK || !strings.Contains(string(sessBody), `"authenticated":true`) ||
		!strings.Contains(string(sessBody), sess.DeviceID) {
		t.Fatalf("session after pairing = %d %s, want the paired device", res.StatusCode, sessBody)
	}

	if err := stopHub(t, h); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := stopHub(t, h); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if got := h.URL(); got != "" {
		t.Fatalf("URL() after Stop = %q, want empty", got)
	}
	if err := h.Start(context.Background()); !errors.Is(err, hub.ErrClosed) {
		t.Fatalf("Start after Stop err = %v, want ErrClosed", err)
	}
	if _, err := h.CreatePairingCode(); !errors.Is(err, hub.ErrClosed) {
		t.Fatalf("CreatePairingCode after Stop err = %v, want ErrClosed", err)
	}
	if _, err := h.ListPairedDevices(); !errors.Is(err, hub.ErrClosed) {
		t.Fatalf("ListPairedDevices after Stop err = %v, want ErrClosed", err)
	}

	// The listener must be gone.
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second); err == nil {
		t.Fatal("port still accepts connections after Stop")
	}
}

// TestBindFailureIsSynchronousAndLeavesResourcesStoppable ports: an occupied
// port fails Start, mDNS is never reached, the external owner is untouched and
// Stop still releases the database and data-dir lock.
func TestBindFailureIsSynchronousAndLeavesResourcesStoppable(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy port: %v", err)
	}
	defer blocker.Close()
	addr := blocker.Addr().String()

	opts := testOptions(t, func(o *hub.Options) { o.Listen = addr })
	h := mustNew(t, opts)

	err = h.Start(context.Background())
	if err == nil {
		t.Fatal("Start on an occupied port returned nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "in use") {
		t.Fatalf("Start err = %v, want a clear address-in-use message", err)
	}
	if strings.Contains(err.Error(), "password") {
		t.Fatal("Start error mentioned a credential")
	}
	if got := h.URL(); got != "" {
		t.Fatalf("URL() after failed Start = %q, want empty", got)
	}
	if err := h.Start(context.Background()); !errors.Is(err, hub.ErrAlreadyStarted) {
		t.Fatalf("Start retry after bind failure err = %v, want ErrAlreadyStarted", err)
	}
	// The Hub is not owned by the blocker; resources are still stoppable.
	// A Start that never bound is "not started", not "closed".
	if _, herr := h.CreatePairingCode(); !errors.Is(herr, hub.ErrNotStarted) {
		t.Fatalf("CreatePairingCode after failed Start = %v, want ErrNotStarted", herr)
	}
	stopQuietly(t, h)

	// The external listener must still be alive: we never stop other processes.
	conn, derr := net.DialTimeout("tcp", addr, time.Second)
	if derr != nil {
		t.Fatalf("external listener was disturbed: %v", derr)
	}
	conn.Close()

	// The data-dir lock is released, so a retry (or the desktop) can start.
	blocker.Close()
	opts.Listen = "127.0.0.1:0"
	h2 := mustNew(t, opts)
	startHub(t, h2)
	stopQuietly(t, h2)
}

// TestSecondWriterSameDataDirRejected pins the "no silent second writer"
// rule: a second Hub on the same data dir fails with a typed error and the
// slot is reusable after Stop.
func TestSecondWriterSameDataDirRejected(t *testing.T) {
	opts := testOptions(t)
	h1 := mustNew(t, opts)

	if _, err := hub.New(opts); !errors.Is(err, hub.ErrDataDirInUse) {
		t.Fatalf("second New err = %v, want ErrDataDirInUse", err)
	}
	stopQuietly(t, h1)

	h2 := mustNew(t, opts)
	startHub(t, h2)
	stopQuietly(t, h2)
}

// TestPairingCodeRegenerateInvalidatesPrevious covers the local-code rules that
// replaced the one-time admin password: a newly generated code discards the old
// one and each code is consumed exactly once. (Expiry, the 5-attempt lock,
// leading zeros, wrong-attempt concurrency and token persistence are covered by
// the pairing store tests; this pins the Hub-level flow.)
func TestPairingCodeRegenerateInvalidatesPrevious(t *testing.T) {
	h := mustNew(t, testOptions(t))
	url := startHub(t, h)

	first, err := h.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	second, err := h.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode (regenerate): %v", err)
	}
	for attempt := 0; second.Code == first.Code; attempt++ {
		// A random 4-digit collision is possible; regenerate so the assertion
		// below is about invalidation, not luck.
		if attempt > 50 {
			t.Fatalf("regeneration kept returning the same code %q", first.Code)
		}
		if second, err = h.CreatePairingCode(); err != nil {
			t.Fatalf("CreatePairingCode (regenerate): %v", err)
		}
	}

	pair := func(code string) int {
		res, _ := doJSON(t, http.MethodPost, url+"/api/v1/auth/pair",
			fmt.Sprintf(`{"code":%q,"device_name":"regenerate-test"}`, code),
			func(r *http.Request) { r.Header.Set("Origin", url) })
		return res.StatusCode
	}
	if status := pair(first.Code); status != http.StatusUnauthorized {
		t.Fatalf("old code after regeneration = %d, want 401", status)
	}
	if status := pair(second.Code); status != http.StatusOK {
		t.Fatalf("current code = %d, want 200", status)
	}
	if status := pair(second.Code); status != http.StatusUnauthorized {
		t.Fatalf("already consumed code = %d, want 401", status)
	}
	devices, err := h.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "regenerate-test" {
		t.Fatalf("paired devices = %+v, want exactly the one successful pairing", devices)
	}
}

// TestPairingCodeConcurrentSingleUse makes sure a code cannot be consumed twice
// when several clients race with it.
func TestPairingCodeConcurrentSingleUse(t *testing.T) {
	h := mustNew(t, testOptions(t))
	url := startHub(t, h)
	code, err := h.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}

	const callers = 3
	statuses := make([]int, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, url+"/api/v1/auth/pair",
				strings.NewReader(fmt.Sprintf(`{"code":%q,"device_name":"racer-%d"}`, code.Code, i)))
			if err != nil {
				errs[i] = err
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", url)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				errs[i] = err
				return
			}
			defer res.Body.Close()
			_, _ = io.Copy(io.Discard, res.Body)
			statuses[i] = res.StatusCode
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("pair caller %d: %v", i, err)
		}
	}

	accepted, rejected := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusOK:
			accepted++
		case http.StatusUnauthorized:
			rejected++
		default:
			t.Fatalf("unexpected pair status %d in %v", status, statuses)
		}
	}
	if accepted != 1 || rejected != callers-1 {
		t.Fatalf("accepted=%d rejected=%d, want 1/%d", accepted, rejected, callers-1)
	}
	devices, err := h.ListPairedDevices()
	if err != nil || len(devices) != 1 {
		t.Fatalf("paired devices after race = %+v, %v; want exactly one", devices, err)
	}
}

// TestPairingPersistsAcrossRestart proves the data dir is the durable state: the
// Hub ID, the saved device token and the items survive a Stop/New cycle.
func TestPairingPersistsAcrossRestart(t *testing.T) {
	opts := testOptions(t)

	h1 := mustNew(t, opts)
	url1 := startHub(t, h1)
	sess := pairDevice(t, h1, url1, "restart-test")

	createdRes, created := doJSON(t, http.MethodPost, url1+"/api/v1/items/text",
		`{"type":"text","text":"persists across restart"}`, authHeaders(url1, sess.CSRF, sess.Cookie))
	if createdRes.StatusCode != http.StatusCreated {
		t.Fatalf("create text = %d body=%s", createdRes.StatusCode, created)
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &item); err != nil || item.ID == "" {
		t.Fatalf("decode created item: %v body=%s", err, created)
	}

	stopQuietly(t, h1)
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(url1, "http://"), time.Second); err == nil {
		t.Fatal("port still open after Stop")
	}

	h2 := mustNew(t, opts)
	url2 := startHub(t, h2)
	_, healthBody := health(t, url2)
	if !strings.Contains(healthBody, sess.HubID) {
		t.Fatalf("health after restart = %s, want the persisted hub id %s", healthBody, sess.HubID)
	}

	// The saved token still authenticates, so the device does not have to pair
	// again after a Hub restart; each origin restores its own cookies.
	csrf2, cookie2 := restoreSession(t, url2, sess.Token)
	pageRes, page := doJSON(t, http.MethodGet, url2+"/api/v1/items?limit=100", "",
		authHeaders(url2, csrf2, cookie2))
	if pageRes.StatusCode != http.StatusOK {
		t.Fatalf("list after restart = %d body=%s", pageRes.StatusCode, page)
	}
	if !strings.Contains(string(page), item.ID) {
		t.Fatalf("item %s missing after restart: %s", item.ID, page)
	}
	devices, err := h2.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices after restart: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != sess.DeviceID {
		t.Fatalf("devices after restart = %+v, want the saved device %s", devices, sess.DeviceID)
	}
}

// TestHandlerBeforeStartAndHostAllowlist exercises the desktop integration
// path: Handler works without Start and keeps the Host allowlist.
func TestHandlerBeforeStartAndHostAllowlist(t *testing.T) {
	h := mustNew(t, testOptions(t, func(o *hub.Options) {
		o.AllowedHosts = []string{"desktop.local"}
	}))
	ts := httptest.NewServer(h.Handler())
	defer ts.Close()

	status, body := health(t, ts.URL)
	if status != http.StatusOK || !strings.Contains(body, "flowdrop-hub") {
		t.Fatalf("health via Handler = %d %q, want 200", status, body)
	}
	// Unauthenticated API stays unauthenticated before Start, too.
	res, sessBody := doJSON(t, http.MethodGet, ts.URL+"/api/v1/auth/session", "")
	if res.StatusCode != http.StatusOK || !strings.Contains(string(sessBody), `"authenticated":false`) {
		t.Fatalf("session without pairing = %d %s, want 200 authenticated=false", res.StatusCode, sessBody)
	}
	res, _ = doJSON(t, http.MethodGet, ts.URL+"/api/v1/items", "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("items without pairing = %d, want 401", res.StatusCode)
	}
	// An unknown Host is rejected with 421 (DNS-rebinding guard).
	res, _ = doJSON(t, http.MethodGet, ts.URL+"/api/v1/health", "", func(r *http.Request) {
		r.Host = "evil.example"
	})
	if res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatalf("unknown Host = %d, want 421", res.StatusCode)
	}
	// AllowedHosts override is honored while serving through Handler.
	res, _ = doJSON(t, http.MethodGet, ts.URL+"/api/v1/health", "", func(r *http.Request) {
		r.Host = "desktop.local"
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("allowed Host desktop.local = %d, want 200", res.StatusCode)
	}
}

// TestOptionsOverrideConfigFile verifies "only non-empty Options override".
func TestOptionsOverrideConfigFile(t *testing.T) {
	root := t.TempDir()
	configDataDir := filepath.Join(root, "from-config")
	cfgPath := filepath.Join(root, "config.json")
	cfg := fmt.Sprintf(`{"listen":"127.0.0.1:1","data_dir":%q,"cookie_secure":true}`, configDataDir)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	no := false
	dataDir := filepath.Join(root, "override")
	h := mustNew(t, hub.Options{
		ConfigPath:       cfgPath,
		Listen:           "127.0.0.1:0",
		DataDir:          dataDir,
		CookieSecure:     &no,
		DiscoveryEnabled: &no,
		AllowedHosts:     []string{"desktop.local"},
	})
	url := startHub(t, h)

	if strings.HasSuffix(url, ":1") {
		t.Fatalf("URL() = %q, config listen was not overridden", url)
	}
	// The data dir override must be the one actually used (lock + database).
	if _, err := os.Stat(filepath.Join(dataDir, "hub.lock")); err != nil {
		t.Fatalf("lock file missing in overridden data dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "flowdrop.db")); err != nil {
		t.Fatalf("database missing in overridden data dir: %v", err)
	}
	if _, err := os.Stat(configDataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config data_dir was touched: %v", err)
	}
}

// TestStopReleasesWebSocketConnections proves Stop does not leave a hijacked
// WebSocket (and its database access) open.
func TestStopReleasesWebSocketConnections(t *testing.T) {
	h := mustNew(t, testOptions(t))
	url := startHub(t, h)
	sess := pairDevice(t, h, url, "ws-test")

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/api/v1/events"
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{sess.Cookie}, "Origin": []string{url}},
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.CloseNow()

	var hello map[string]string
	if err := wsjson.Read(dialCtx, conn, &hello); err != nil {
		t.Fatalf("read hello: %v", err)
	}
	if hello["type"] != "hello" {
		t.Fatalf("hello = %v, want type=hello", hello)
	}

	if err := stopHub(t, h); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	var msg map[string]string
	if err := wsjson.Read(readCtx, conn, &msg); err == nil {
		t.Fatal("WebSocket still readable after Stop; connection was leaked")
	}
}

// TestStopWithoutStartReleasesLock allows the desktop to construct a Hub,
// decide not to serve, and hand the data dir to someone else.
func TestStopWithoutStartReleasesLock(t *testing.T) {
	opts := testOptions(t)
	h := mustNew(t, opts)
	// A configured-but-unstarted Hub cannot mint a pairing code; stopping it
	// still releases the database and the data-dir lock for the next owner.
	if _, err := h.CreatePairingCode(); !errors.Is(err, hub.ErrNotStarted) {
		t.Fatalf("CreatePairingCode before Start = %v, want ErrNotStarted", err)
	}
	stopQuietly(t, h)

	h2 := mustNew(t, opts)
	url := startHub(t, h2)
	sess := pairDevice(t, h2, url, "handover-test")
	devices, err := h2.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices after handover: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != sess.DeviceID {
		t.Fatalf("devices after handover = %+v, want the newly paired %s", devices, sess.DeviceID)
	}
}

// TestInvalidOverrideFailsBeforeTouchingDisk proves Options overrides are
// validated before the data dir is created.
func TestInvalidOverrideFailsBeforeTouchingDisk(t *testing.T) {
	no := false
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	_, err := hub.New(hub.Options{
		ConfigPath:   filepath.Join(dir, "absent-config.json"),
		Listen:       "0.0.0.0:8080",
		DataDir:      dataDir,
		CookieSecure: &no,
	})
	if err == nil {
		t.Fatal("New accepted a wildcard listen with cookie_secure=false")
	}
	if _, statErr := os.Stat(dataDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("data dir was created before validation: %v", statErr)
	}
}

// TestConcurrentStopIsIdempotent exercises the documented Stop semantics.
func TestConcurrentStopIsIdempotent(t *testing.T) {
	h := mustNew(t, testOptions(t))
	startHub(t, h)

	const callers = 4
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			errs[i] = h.Stop(ctx)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Stop caller %d: %v", i, err)
		}
	}
}
