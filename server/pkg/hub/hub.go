// Package hub exposes the FlowDrop Hub as an embeddable, in-process lifecycle
// so the CLI (cmd/flowdrop) and the Wails desktop bridge share exactly one
// implementation of the Hub instead of copying the business logic.
//
// The public surface is deliberately small and frozen for the desktop worker:
//
//	h, err := hub.New(hub.Options{Listen: "127.0.0.1:0"})
//	if err != nil { ... }
//	if err := h.Start(ctx); err != nil { ... }   // returns once every port is bound
//	defer h.Stop(context.Background())
//	http.Handle("/", h.Handler())                // same Host/Origin/CSRF checks
//	for _, u := range h.URLs() { ... }           // real ports, primary first
//
// Lifecycle contract:
//
//   - New loads configuration (file, FLOWDROP_* env, then non-zero Options
//     fields), takes the data-directory write lock, opens SQLite/attachments and
//     builds the HTTP handler. A failure cleans up everything it acquired.
//   - Start binds the primary and additional listen addresses synchronously. If
//     any bind fails, listeners bound earlier in that call are closed again and
//     an error is returned: there is no half-started Hub. Address-in-use,
//     invalid config or an already cancelled context return an error; mDNS is
//     only published after every TCP bind succeeded, so a failed port is never
//     advertised. Start may be called exactly once.
//   - Stop is idempotent, withdraws mDNS, cancels the janitor, gracefully closes
//     every HTTP listener and WebSocket client, closes the database and releases
//     the data-dir lock. It only touches this instance; an externally running
//     process is never signalled or killed.
//   - A failed Start may be followed by Stop to release resources. To change the
//     listen address, Stop the old Hub and New/Start a new one against the same
//     DataDir (the lock is released by Stop).
//
// The HTTP handler keeps the existing authentication model (session cookie,
// CSRF double-submit token, Host/Origin allowlist) with device pairing as the
// login path: a locally generated code yields a session plus a per-device token.
// Pairing administration (create code, list, revoke) is local-only through the
// methods in pairing.go. There is no admin password or admin-initialisation
// entry point: pairing is the only authentication path.
package hub

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"flowdrop/internal/api"
	"flowdrop/internal/auth"
	"flowdrop/internal/config"
	"flowdrop/internal/db"
	"flowdrop/internal/discovery"
	"flowdrop/internal/events"
	"flowdrop/internal/pairing"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
	"flowdrop/internal/webui"
)

const (
	// shutdownGrace bounds an otherwise unbounded Stop context.
	shutdownGrace = 10 * time.Second
	// wsDrainWait bounds how long Stop waits for WebSocket subscribers to leave
	// before the database is closed underneath them.
	wsDrainWait = 3 * time.Second
	// janitorInterval matches the CLI default housekeeping cadence.
	janitorInterval = 15 * time.Minute
	// lockFileName is the advisory write-lock inside DataDir.
	lockFileName = "hub.lock"
)

var (
	// ErrAlreadyStarted is returned by Start on a Hub that already bound once.
	ErrAlreadyStarted = errors.New("hub is already started")
	// ErrClosed is returned by methods that need live resources after Stop.
	ErrClosed = errors.New("hub is stopped")
	// ErrDataDirInUse means another live FlowDrop Hub (CLI process or desktop
	// bridge) already holds the write lock for this data directory.
	ErrDataDirInUse = errors.New("data directory is in use by another FlowDrop Hub")
)

// Options configures New. Every zero value means "keep what the configuration
// file and FLOWDROP_* environment already decided":
//
//   - ConfigPath empty  -> DefaultConfigPath()
//   - Listen empty      -> configured listen address
//   - AdditionalListen nil -> configured additional_listen (a non-nil empty
//     slice explicitly overrides it to "no additional listeners")
//   - DataDir empty     -> configured data_dir
//   - CookieSecure nil  -> configured cookie_secure
//   - DiscoveryEnabled nil -> configured discovery_enabled (tri-state kept)
//   - AllowedHosts nil  -> configured allowed_hosts (a non-nil empty slice
//     explicitly overrides it to "loopback only")
type Options struct {
	// ConfigPath is the JSON config file to load (may not exist).
	ConfigPath string
	// Listen overrides the primary listen address, e.g. "127.0.0.1:0" for an
	// ephemeral desktop port. Use a specific IP:port, never 0.0.0.0 with
	// plaintext LAN mode. It stays the address reported by URL().
	Listen string
	// AdditionalListen overrides additional explicit host:port endpoints bound
	// by this same Hub (one store/database/auth/broker). nil keeps the configured
	// additional_listen; a non-nil slice (including an empty one) replaces it.
	// Each entry must be a literal, non-wildcard IP; same-port/different-IP is
	// allowed. An exact duplicate of the primary address is bound once. Port 0
	// asks the OS for a port; every :0 listener gets its own ephemeral port and
	// URLs() reports the actual ones.
	AdditionalListen []string
	// DataDir overrides the data directory (database + attachments + lock).
	DataDir string
	// CookieSecure overrides the Secure cookie flag. The desktop loopback
	// bridge usually wants false for plain http://127.0.0.1.
	CookieSecure *bool
	// DiscoveryEnabled overrides the mDNS tri-state. Discovery still only
	// publishes an eligible plaintext LAN IPv4 bind.
	DiscoveryEnabled *bool
	// AllowedHosts overrides the Host allowlist. nil keeps configuration.
	AllowedHosts []string
}

// Hub is one embeddable FlowDrop Hub instance. Methods are safe for concurrent
// use unless noted; the documented lifecycle (New -> Start -> Stop) is linear.
type Hub struct {
	cfg config.Config
	log *slog.Logger

	conn    *sql.DB
	files   *storage.Store
	store   *store.Store
	sess    *auth.Store
	pairing *pairing.Store
	broker  *events.Broker
	api     *api.Server
	handler http.Handler
	lock    *dataDirLock

	// hubID is the stable, persisted pairing identity (not a secret) and
	// hostname the advertised display name. Both are resolved once in New so
	// the API metadata callback never has to touch the database.
	hubID    string
	hostname string

	mu             sync.Mutex
	startAttempted bool
	started        bool
	stopped        bool
	srv            *http.Server
	listeners      []net.Listener
	ctx            context.Context
	cancel         context.CancelFunc
	disc           *discovery.Handle
	janitor        chan struct{} // closed when the janitor goroutine exits
	baseURL        string
	urls           []string

	stopOnce sync.Once
	stopDone chan struct{}
	stopErr  error
}

// New loads the effective configuration and prepares the Hub: data-dir lock,
// SQLite schema, attachment storage, sessions, event broker and HTTP handler.
// Nothing is bound to the network yet, so Handler is already usable (for
// example mounted in the desktop webview) before Start.
//
// Any error closes whatever was already opened, including the data-dir lock.
func New(opts Options) (*Hub, error) {
	cfgPath := opts.ConfigPath
	if cfgPath == "" {
		cfgPath = DefaultConfigPath()
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	// Only non-zero Options override configuration/env, so the desktop bridge
	// can set just the port and keep everything else operator-managed.
	if opts.Listen != "" {
		cfg.Listen = opts.Listen
	}
	if opts.AdditionalListen != nil {
		cfg.AdditionalListen = append([]string(nil), opts.AdditionalListen...)
	}
	if opts.DataDir != "" {
		cfg.DataDir = opts.DataDir
	}
	if opts.CookieSecure != nil {
		cfg.CookieSecure = *opts.CookieSecure
	}
	if opts.DiscoveryEnabled != nil {
		v := *opts.DiscoveryEnabled
		cfg.DiscoveryEnabled = &v
	}
	if opts.AllowedHosts != nil {
		cfg.AllowedHosts = append([]string(nil), opts.AllowedHosts...)
	}
	// Overrides can make a previously valid config invalid (wildcard +
	// plaintext, empty data_dir, malformed host:port).
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	log := newLogger()
	h := &Hub{
		cfg:      cfg,
		log:      log,
		stopDone: make(chan struct{}),
	}

	ok := false
	defer func() {
		if !ok {
			h.release()
		}
	}()

	// Take the write lock before the database so a second writer fails with a
	// clear message instead of silently sharing SQLite/attachments.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	lock, err := lockDataDir(filepath.Join(cfg.DataDir, lockFileName))
	if err != nil {
		if errors.Is(err, ErrDataDirInUse) {
			return nil, fmt.Errorf("%w: %s (stop the other FlowDrop Hub/CLI or choose a different data_dir)",
				ErrDataDirInUse, cfg.DataDir)
		}
		return nil, fmt.Errorf("lock data dir %s: %w", cfg.DataDir, err)
	}
	h.lock = lock

	conn, err := db.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	h.conn = conn

	files, err := storage.New(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	h.files = files
	h.store = store.New(conn)
	h.sess = auth.NewStore(conn, cfg.SessionTTL())

	// Device pairing is the authentication path: no admin password has to be
	// initialized before a local client can pair. The hub ID is generated and
	// persisted on first use, so it survives restarts; the name is the OS
	// hostname with a fixed fallback.
	h.pairing = pairing.NewStore(conn)
	h.hubID, err = h.pairing.HubID()
	if err != nil {
		return nil, err
	}
	if name, err := os.Hostname(); err == nil {
		h.hostname = strings.TrimSpace(name)
	}

	h.broker = events.NewBroker()
	h.api = api.New(api.Deps{
		Config:         cfg,
		Sessions:       h.sess,
		Store:          h.store,
		Storage:        files,
		Broker:         h.broker,
		Pairing:        h.pairing,
		HubInfo:        h.hubInfo,
		UI:             webui.FS(),
		Logger:         log,
		TrustedProxies: parseTrustedProxies(cfg.TrustedProxies),
	})
	h.handler = h.api.Handler()

	ok = true
	log.Info("flowdrop hub configured",
		"version", api.Version,
		"listen", cfg.Listen,
		"additional_listen", cfg.AdditionalListen,
		"data_dir", cfg.DataDir,
		"cookie_secure", cfg.CookieSecure,
		"hub_id", h.hubID,
		"pairing", "device-code",
	)
	return h, nil
}

// Handler returns the fully wrapped HTTP handler: Host/Origin allowlist,
// security headers, session auth, CSRF and the embedded SPA. It is valid from
// New until Stop and never bypasses authentication for desktop callers.
func (h *Hub) Handler() http.Handler {
	return h.handler
}

// URL reports the real HTTP base URL of the primary bound listener
// ("http://host:port") between a successful Start and Stop, and "" otherwise.
// It reflects the actual port, so Listen "127.0.0.1:0" yields the ephemeral
// port the OS assigned. With additional listeners, URL is always URLs()[0].
func (h *Hub) URL() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped || !h.started {
		return ""
	}
	return h.baseURL
}

// URLs reports the real HTTP base URL of every bound listener, primary first in
// the configured Start order. The returned slice is a copy: callers may keep
// or mutate it without affecting the Hub. It is nil before a successful Start
// and after Stop, so it never reports a listener that is not accepting.
func (h *Hub) URLs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped || !h.started {
		return nil
	}
	return append([]string(nil), h.urls...)
}

// Start binds every configured listen address synchronously and begins serving.
// When it returns nil all ports are already accepting connections and URL/URLs
// are valid; if any binding fails (port in use, invalid address, permission)
// the listeners already bound by this call are closed again, an error is
// returned and nothing is published via mDNS. All listeners share one handler,
// database, attachment store, auth session store, broker and rate limiters.
//
// ctx is the Hub lifetime: when it is cancelled the Hub shuts itself down
// (equivalent to Stop). Start is attempted at most once per Hub, even when a
// bind failed; to switch addresses call Stop, then New and Start a new Hub
// against the same DataDir. A failed Start leaves the database and data-dir
// lock held until Stop, so callers must Stop to release them.
func (h *Hub) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return ErrClosed
	}
	if h.startAttempted {
		h.mu.Unlock()
		return ErrAlreadyStarted
	}
	if err := ctx.Err(); err != nil {
		h.mu.Unlock()
		return err
	}
	h.startAttempted = true
	// Bind every address here, synchronously. A Hub that can only reach one of
	// its configured interfaces would be a silent half-start, so any failure
	// rolls back the listeners already bound in this call and returns an error
	// before HTTP, the janitor or mDNS start.
	listens := h.cfg.Listens()
	bound := make([]net.Listener, 0, len(listens))
	for _, addr := range listens {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, done := range bound {
				_ = done.Close()
			}
			h.mu.Unlock()
			startErr := fmt.Errorf("listen on %s: %w%s", addr, err, listenHint(err))
			if len(bound) > 0 {
				return fmt.Errorf("%w (rolled back %d already bound listener(s))", startErr, len(bound))
			}
			return startErr
		}
		bound = append(bound, ln)
	}
	urls := make([]string, len(bound))
	for i, ln := range bound {
		urls[i] = listenerURL(ln)
	}
	h.listeners = bound
	h.urls = urls
	h.baseURL = urls[0]
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.janitor = make(chan struct{})
	h.started = true

	h.srv = &http.Server{
		Handler:           h.handler,
		ReadHeaderTimeout: 15 * time.Second,
		// No WriteTimeout: large downloads and WebSocket need long-lived writes.
		IdleTimeout: 120 * time.Second,
		BaseContext: func(net.Listener) context.Context { return h.ctx },
		ErrorLog:    slog.NewLogLogger(h.log.Handler(), slog.LevelWarn),
	}
	srv := h.srv
	hubCtx := h.ctx
	janitorDone := h.janitor
	h.mu.Unlock()

	h.warnAboutNetworkMode()

	// One http.Server serves every listener, so all of them share the exact same
	// handler, store, auth sessions, broker, rate limiters and upload semaphore.
	for _, ln := range bound {
		ln := ln
		addr := ln.Addr().String()
		go func() {
			err := srv.Serve(ln)
			if err == nil || errors.Is(err, http.ErrServerClosed) {
				return
			}
			// A concurrent Stop closes the listener underneath Serve; that is a
			// normal shutdown, not a failure.
			h.mu.Lock()
			stopped := h.stopped
			h.mu.Unlock()
			if !stopped {
				h.log.Error("http server stopped unexpectedly", "listen", addr, "err", err)
			}
		}()
	}

	go func() {
		defer close(janitorDone)
		// Stop may have won the race before this goroutine started; never run
		// housekeeping (it passes the first pass unconditionally) after the
		// database was closed.
		if hubCtx.Err() != nil {
			return
		}
		janitor := &api.Janitor{
			Sessions: h.sess,
			Store:    h.store,
			Storage:  h.files,
			Logger:   h.log,
		}
		janitor.Run(hubCtx, janitorInterval)
	}()

	// mDNS is published only after every successful bind, so a Hub that failed
	// to get a port never appears on the LAN. Advertised ports are the ones the
	// OS actually assigned (relevant for :0), and only eligible RFC1918 plain
	// HTTP listeners are announced.
	boundAddrs := make([]string, len(bound))
	for i, ln := range bound {
		boundAddrs[i] = ln.Addr().String()
	}
	disc := startDiscovery(hubCtx, h.cfg, boundAddrs, h.log)
	h.mu.Lock()
	if h.stopped {
		// Stop won the race while discovery was registering; withdraw
		// immediately instead of leaking a published record.
		h.mu.Unlock()
		if disc != nil {
			disc.Close()
		}
		return nil
	}
	h.disc = disc
	startedURL := h.baseURL
	startedURLs := append([]string(nil), h.urls...)
	h.mu.Unlock()

	// Cancelling ctx tears the Hub down; explicit Stop also closes hubCtx.
	go func() {
		select {
		case <-ctx.Done():
			if err := h.Stop(context.Background()); err != nil {
				h.log.Warn("hub shutdown after context cancellation", "err", err)
			}
		case <-hubCtx.Done():
		}
	}()

	h.log.Info("listening", "url", startedURL, "urls", startedURLs)
	return nil
}

// Stop shuts this instance down: it withdraws mDNS first (so clients stop
// resolving the entry), cancels in-flight requests and WebSockets, waits for
// the janitor, shuts the HTTP server down gracefully and closes the database and
// data-dir lock. It is idempotent and safe to call after a failed Start or
// without Start at all; repeated calls return the first result. The context
// bounds the HTTP shutdown (a default 10s is applied when it has no deadline).
func (h *Hub) Stop(ctx context.Context) error {
	h.stopOnce.Do(func() {
		h.stopErr = h.shutdown(ctx)
		close(h.stopDone)
	})
	<-h.stopDone
	return h.stopErr
}

func (h *Hub) shutdown(ctx context.Context) error {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return nil
	}
	wasStarted := h.started
	h.stopped = true
	disc := h.disc
	h.disc = nil
	srv := h.srv
	listeners := append([]net.Listener(nil), h.listeners...)
	h.listeners = nil
	h.urls = nil
	h.baseURL = ""
	cancel := h.cancel
	janitorDone := h.janitor
	h.mu.Unlock()

	// Withdraw the mDNS record before HTTP closes so clients do not keep a
	// stale entry pointing at a socket that is already going away.
	if disc != nil {
		disc.Close()
	}
	// Cancelling closes WebSocket request contexts and stops the janitor.
	if cancel != nil {
		cancel()
	}

	var err error
	if srv != nil {
		shutdownCtx, cancelTimeout := withDefaultTimeout(ctx, shutdownGrace)
		err = srv.Shutdown(shutdownCtx)
		cancelTimeout()
	}
	// Shutdown closes tracked listeners, but a Serve goroutine that lost the
	// Start/Stop race may not have registered its listener yet. Closing the
	// saved listeners explicitly guarantees Stop releases every port before it
	// returns (double Close just reports an already-closed socket, ignored).
	for _, ln := range listeners {
		_ = ln.Close()
	}

	// Hijacked WebSocket connections are not tracked by Shutdown; wait briefly
	// for their subscribers to unsubscribe before closing the database.
	h.waitForSubscribers()

	if janitorDone != nil {
		<-janitorDone
	}
	h.release()
	if wasStarted {
		h.log.Info("hub stopped")
	} else {
		h.log.Info("hub resources released")
	}
	return err
}

// release closes the database and the data-dir lock. Idempotent.
func (h *Hub) release() {
	if h.conn != nil {
		if err := h.conn.Close(); err != nil {
			h.log.Warn("close database", "err", err)
		}
		h.conn = nil
	}
	if h.lock != nil {
		if err := h.lock.Close(); err != nil {
			h.log.Warn("release data dir lock", "err", err)
		}
		h.lock = nil
	}
}

func (h *Hub) waitForSubscribers() {
	if h.broker == nil {
		return
	}
	deadline := time.Now().Add(wsDrainWait)
	for h.broker.SubscriberCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
}

// warnAboutNetworkMode mirrors the CLI's plaintext-LAN guidance for every
// listener of this Hub, not just the primary address.
func (h *Hub) warnAboutNetworkMode() {
	cfg := h.cfg
	var remote, wildcards []string
	for _, addr := range cfg.Listens() {
		if !config.IsLoopbackListen(addr) {
			remote = append(remote, addr)
		}
		if config.IsWildcardListen(addr) {
			wildcards = append(wildcards, addr)
		}
	}
	if len(remote) == 0 {
		return
	}
	if cfg.CookieSecure {
		h.log.Warn("listening on a non-loopback address with Secure cookies; plaintext HTTP clients will not keep a session",
			"listens", remote, "hint", "set cookie_secure=false only for a trusted LAN, or terminate TLS in front")
	} else {
		h.log.Warn("plaintext HTTP LAN mode: passwords, session cookies and content are not encrypted on the wire",
			"listens", remote, "hint", "use only on a trusted network; prefer Tailscale or a TLS reverse proxy elsewhere")
	}
	if len(wildcards) > 0 {
		h.log.Warn("listen address is a wildcard; bind the specific LAN IP instead of 0.0.0.0/:: to avoid exposing every interface",
			"listens", wildcards)
	}
}

// DefaultConfigPath returns the same config location the CLI uses:
// FLOWDROP_CONFIG, then XDG_CONFIG_HOME/flowdrop/config.json, then
// ~/.config/flowdrop/config.json.
func DefaultConfigPath() string {
	if v := os.Getenv("FLOWDROP_CONFIG"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "flowdrop", "config.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.json"
	}
	return filepath.Join(home, ".config", "flowdrop", "config.json")
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if v := os.Getenv("FLOWDROP_LOG_LEVEL"); v != "" {
		_ = level.UnmarshalText([]byte(v))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// startDiscovery publishes mDNS records for the eligible LAN listeners when the
// configuration allows it. listens are the actual bound addresses, so a :0 port
// is advertised correctly. Discovery is optional: every failure is logged and
// the Hub keeps serving normal HTTP traffic.
func startDiscovery(ctx context.Context, cfg config.Config, listens []string, log *slog.Logger) *discovery.Handle {
	if len(listens) == 0 {
		return nil
	}
	handle, err := discovery.Start(ctx, discovery.Options{
		Enabled:          cfg.DiscoveryOn(),
		Listen:           listens[0],
		AdditionalListen: listens[1:],
		CookieSecure:     cfg.CookieSecure,
		Logger:           log,
	})
	switch {
	case err == nil:
		return handle
	case errors.Is(err, discovery.ErrDisabled):
		log.Info("mDNS LAN discovery disabled by configuration")
	case errors.Is(err, discovery.ErrIneligible):
		if cfg.DiscoveryExplicit() && cfg.DiscoveryOn() {
			log.Warn("mDNS LAN discovery requested but none of the listen addresses is eligible", "reason", err)
		} else {
			log.Info("mDNS LAN discovery not published for these listen addresses", "reason", err)
		}
	default:
		log.Warn("mDNS LAN discovery could not start; the Hub keeps serving without auto-discovery", "err", err)
	}
	return nil
}

func parseTrustedProxies(entries []string) []*net.IPNet {
	var trusted []*net.IPNet
	for _, p := range entries {
		if _, n, err := net.ParseCIDR(p); err == nil {
			trusted = append(trusted, n)
		} else if ip := net.ParseIP(p); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			trusted = append(trusted, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	return trusted
}

// listenerURL turns a bound listener into the URL clients should use. A wildcard
// bind is reported as loopback because that is the address a local desktop
// bridge must dial.
func listenerURL(ln net.Listener) string {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "http://" + ln.Addr().String()
	}
	ip := addr.IP
	if ip == nil || ip.IsUnspecified() {
		ip = net.IPv4(127, 0, 0, 1)
	}
	return "http://" + net.JoinHostPort(ip.String(), strconv.Itoa(addr.Port))
}

// listenHint adds the actionable cause for the most common Start failure.
func listenHint(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if isAddrInUse(opErr.Err) {
			return " (the port is already taken, possibly by a running FlowDrop Hub/CLI; stop that process or pick another port; this process will not stop it)"
		}
	}
	return ""
}

// withDefaultTimeout applies d when ctx has no deadline.
func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), d)
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}
