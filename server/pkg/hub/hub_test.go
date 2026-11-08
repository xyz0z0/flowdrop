package hub_test

import (
	"context"
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

const testPassword = "correct horse battery staple"

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

func login(t *testing.T, baseURL, username, password string) (*http.Response, []byte) {
	t.Helper()
	return doJSON(t, http.MethodPost, baseURL+"/api/v1/auth/login",
		fmt.Sprintf(`{"username":%q,"password":%q}`, username, password),
		func(r *http.Request) { r.Header.Set("Origin", baseURL) })
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
	if has, err := h.HasAdmin(); err != nil || has {
		t.Fatalf("HasAdmin() = %v, %v; want false, nil", has, err)
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

	// Login must still be refused before an admin exists.
	res, _ := login(t, url, "admin", testPassword)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("login before init = %d, want 403", res.StatusCode)
	}

	if err := h.InitializeAdmin("", testPassword); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	if has, err := h.HasAdmin(); err != nil || !has {
		t.Fatalf("HasAdmin() = %v, %v; want true, nil", has, err)
	}
	res, _ = login(t, url, "admin", testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login after init = %d, want 200", res.StatusCode)
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
	if _, err := h.HasAdmin(); !errors.Is(err, hub.ErrClosed) {
		t.Fatalf("HasAdmin after Stop err = %v, want ErrClosed", err)
	}
	if err := h.InitializeAdmin("admin", testPassword); !errors.Is(err, hub.ErrClosed) {
		t.Fatalf("InitializeAdmin after Stop err = %v, want ErrClosed", err)
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
	if strings.Contains(err.Error(), testPassword) {
		t.Fatal("Start error leaked a password")
	}
	if got := h.URL(); got != "" {
		t.Fatalf("URL() after failed Start = %q, want empty", got)
	}
	if err := h.Start(context.Background()); !errors.Is(err, hub.ErrAlreadyStarted) {
		t.Fatalf("Start retry after bind failure err = %v, want ErrAlreadyStarted", err)
	}
	// The Hub is not owned by the blocker; resources are still stoppable.
	if has, herr := h.HasAdmin(); herr != nil || has {
		t.Fatalf("HasAdmin after failed Start = %v, %v; want false, nil", has, herr)
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

// TestInitializeAdminOnlyOnce covers the local-only initialization rule.
func TestInitializeAdminOnlyOnce(t *testing.T) {
	h := mustNew(t, testOptions(t))
	url := startHub(t, h)

	if err := h.InitializeAdmin("admin", "short"); err == nil {
		t.Fatal("InitializeAdmin accepted a too-short password")
	}
	if has, err := h.HasAdmin(); err != nil || has {
		t.Fatalf("HasAdmin after rejected password = %v, %v; want false, nil", has, err)
	}

	if err := h.InitializeAdmin("admin", testPassword); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	if err := h.InitializeAdmin("admin", "another-long-password"); !errors.Is(err, hub.ErrAdminExists) {
		t.Fatalf("second InitializeAdmin err = %v, want ErrAdminExists", err)
	}
	// The original password must still be the valid one.
	res, _ := login(t, url, "admin", testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login with original password = %d, want 200 (password was overwritten)", res.StatusCode)
	}
	res, _ = login(t, url, "admin", "another-long-password")
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with rejected password = %d, want 401", res.StatusCode)
	}
}

// TestInitializeAdminConcurrent makes sure parallel bridge calls cannot create
// two admins.
func TestInitializeAdminConcurrent(t *testing.T) {
	h := mustNew(t, testOptions(t))
	startHub(t, h)

	const callers = 3
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = h.InitializeAdmin("admin", testPassword)
		}(i)
	}
	wg.Wait()

	created, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, hub.ErrAdminExists):
			refused++
		default:
			t.Fatalf("unexpected InitializeAdmin error: %v", err)
		}
	}
	if created != 1 || refused != callers-1 {
		t.Fatalf("created=%d refused=%d, want 1/%d", created, refused, callers-1)
	}
}

// TestAdminPersistsAcrossRestart proves the data dir is the durable state.
func TestAdminPersistsAcrossRestart(t *testing.T) {
	opts := testOptions(t)

	h1 := mustNew(t, opts)
	url1 := startHub(t, h1)
	if err := h1.InitializeAdmin("admin", testPassword); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	stopQuietly(t, h1)
	if _, err := net.DialTimeout("tcp", strings.TrimPrefix(url1, "http://"), time.Second); err == nil {
		t.Fatal("port still open after Stop")
	}

	h2 := mustNew(t, opts)
	if has, err := h2.HasAdmin(); err != nil || !has {
		t.Fatalf("HasAdmin after restart = %v, %v; want true, nil", has, err)
	}
	if err := h2.InitializeAdmin("admin", testPassword); !errors.Is(err, hub.ErrAdminExists) {
		t.Fatalf("InitializeAdmin after restart err = %v, want ErrAdminExists", err)
	}
	url2 := startHub(t, h2)
	res, _ := login(t, url2, "admin", testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login after restart = %d, want 200", res.StatusCode)
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
	// Unauthenticated API stays protected before Start, too.
	res, _ := login(t, ts.URL, "admin", testPassword)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("login without admin = %d, want 403", res.StatusCode)
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
	if err := h.InitializeAdmin("admin", testPassword); err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	res, _ := login(t, url, "admin", testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", res.StatusCode)
	}
	var cookiePairs []string
	for _, c := range res.Cookies() {
		cookiePairs = append(cookiePairs, c.Name+"="+c.Value)
	}
	if len(cookiePairs) == 0 {
		t.Fatal("login set no cookies")
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	wsURL := "ws" + strings.TrimPrefix(url, "http") + "/api/v1/events"
	conn, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{strings.Join(cookiePairs, "; ")}},
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
	if err := h.InitializeAdmin("admin", testPassword); err != nil {
		t.Fatalf("InitializeAdmin before Start: %v", err)
	}
	stopQuietly(t, h)

	h2 := mustNew(t, opts)
	if has, err := h2.HasAdmin(); err != nil || !has {
		t.Fatalf("HasAdmin after handover = %v, %v; want true, nil", has, err)
	}
	url := startHub(t, h2)
	res, _ := login(t, url, "admin", testPassword)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login after handover = %d, want 200", res.StatusCode)
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
