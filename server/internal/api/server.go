package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"flowdrop/internal/config"
	"flowdrop/internal/events"
	"flowdrop/internal/pairing"
	"flowdrop/internal/storage"
	"flowdrop/internal/store"
)

const (
	sessionCookie = "flowdrop_session"
	csrfCookie    = "flowdrop_csrf"
	csrfHeader    = "X-FlowDrop-CSRF"

	maxTextBytes = 64 << 10
)

type Server struct {
	cfg config.Config
	// pairing is the only authentication source for API, WS, preview and
	// download requests.
	pairing *pairing.Store
	hubInfo func() HubInfo
	store   *store.Store
	files   *storage.Store
	broker  *events.Broker
	ui      fs.FS
	limiter *limiter
	// pairLimiter bounds pairing attempts per source IP (PairLimitPerMinute);
	// limiter stays the generic API limit.
	pairLimiter *limiter
	log         *slog.Logger

	uploadSem    chan struct{}
	trusted      []*net.IPNet
	allowedHosts map[string]struct{}

	// boundHosts is the Host set derived from the Hub's currently bound
	// listeners; SetBoundHosts replaces it wholesale as listeners come and go.
	// allowedHosts above is static and always stays valid.
	boundMu    sync.RWMutex
	boundHosts map[string]struct{}

	now func() time.Time

	// uploadTimeout bounds a single upload request, including slow clients.
	uploadTimeout time.Duration
}

type Deps struct {
	Config config.Config
	// Pairing is the device-pairing store; it is the only authentication
	// source for API, WS, preview and download requests.
	Pairing *pairing.Store
	// HubInfo may be nil: without it the Hub reports its persistent ID and an
	// empty endpoint list, never an address it has not actually bound.
	HubInfo        func() HubInfo
	Store          *store.Store
	Storage        *storage.Store
	Broker         *events.Broker
	UI             fs.FS
	Logger         *slog.Logger
	TrustedProxies []*net.IPNet
}

func New(d Deps) *Server {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Server{
		cfg:           d.Config,
		pairing:       d.Pairing,
		hubInfo:       d.HubInfo,
		store:         d.Store,
		files:         d.Storage,
		broker:        d.Broker,
		ui:            d.UI,
		log:           log,
		uploadSem:     make(chan struct{}, d.Config.MaxConcurrentUploads),
		trusted:       d.TrustedProxies,
		allowedHosts:  buildAllowedHosts(d.Config),
		now:           time.Now,
		uploadTimeout: 15 * time.Minute,
	}
	s.limiter = newLimiter(d.Config.APILimitPerMinute, time.Minute)
	s.pairLimiter = newLimiter(d.Config.PairLimitPerMinute, time.Minute)
	return s
}

func buildAllowedHosts(cfg config.Config) map[string]struct{} {
	m := map[string]struct{}{
		"localhost": {},
		"127.0.0.1": {},
		"::1":       {},
	}
	// The concrete IP of every explicitly bound listener is always a valid Host:
	// a LAN/HTTP deployment browses http://<lan-ip>:<port> and would otherwise
	// get a 421 even though that exact address was configured. This covers the
	// primary and all additional listeners of the same Hub. Wildcard binds
	// (0.0.0.0 / :: / empty) are deliberately NOT added, so "listen on every
	// interface" still requires naming the reachable Hosts explicitly.
	for _, listen := range cfg.Listens() {
		if host, _, err := net.SplitHostPort(listen); err == nil && host != "" {
			if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
				m[ip.String()] = struct{}{}
			}
		}
	}
	for _, h := range cfg.AllowedHosts {
		m[strings.ToLower(h)] = struct{}{}
	}
	return m
}

// SetBoundHosts replaces the Host values derived from the Hub's currently
// bound listeners. It is safe to call concurrently with serving requests and
// may be called repeatedly as listeners come and go. An empty slice clears the
// dynamic set; hostnames from config (allowed_hosts) and the loopback names
// stay valid regardless.
func (s *Server) SetBoundHosts(hosts []string) {
	next := make(map[string]struct{}, len(hosts))
	for _, raw := range hosts {
		if host := normalizeBoundHost(raw); host != "" {
			next[host] = struct{}{}
		}
	}
	s.boundMu.Lock()
	s.boundHosts = next
	s.boundMu.Unlock()
}

// normalizeBoundHost applies hostCheck's Host normalization (drop the port,
// lower-case, strip IPv6 brackets) and rejects values that must never widen the
// allow-list: empty strings, anything carrying a path/space, and wildcard binds
// (0.0.0.0 / ::), which are deliberately not trusted just because we bound them.
func normalizeBoundHost(raw string) string {
	h := raw
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "" || strings.ContainsAny(h, "/ ") {
		return ""
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsUnspecified() {
		return ""
	}
	return h
}

// hostAllowed reports whether a normalized host (lower-case, no port, no
// brackets) is acceptable as a Host header or Origin host. The static set from
// config is checked first, then the listener-derived dynamic set.
func (s *Server) hostAllowed(host string) bool {
	if _, ok := s.allowedHosts[host]; ok {
		return true
	}
	s.boundMu.RLock()
	_, ok := s.boundHosts[host]
	s.boundMu.RUnlock()
	return ok
}

// Handler builds the full route tree with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("POST /api/v1/auth/pair", s.handlePair)
	mux.HandleFunc("GET /api/v1/auth/session", s.handleSessionInfo)
	mux.HandleFunc("POST /api/v1/auth/logout", s.withAuth(s.withCSRF(s.handleLogout)))
	mux.HandleFunc("GET /api/v1/items", s.withAuth(s.handleListItems))
	mux.HandleFunc("POST /api/v1/items/text", s.withAuth(s.withCSRF(s.handleCreateText)))
	mux.HandleFunc("POST /api/v1/items/upload", s.withAuth(s.withCSRF(s.handleUpload)))
	mux.HandleFunc("GET /api/v1/items/{id}", s.withAuth(s.handleGetItem))
	mux.HandleFunc("DELETE /api/v1/items/{id}", s.withAuth(s.withCSRF(s.handleDeleteItem)))
	mux.HandleFunc("GET /api/v1/items/{id}/download", s.withAuth(s.handleDownload))
	mux.HandleFunc("GET /api/v1/items/{id}/preview", s.withAuth(s.handlePreview))
	mux.HandleFunc("GET /api/v1/events", s.handleEvents)

	mux.Handle("/", s.uiHandler())

	return s.securityHeaders(s.hostCheck(s.logRequests(mux)))
}

// ---------- middleware ----------

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "clipboard-write=(self), geolocation=(), microphone=(), camera=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if s.isHTTPS(r) {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy",
				"default-src 'self'; img-src 'self' blob: data:; style-src 'self' 'unsafe-inline'; "+
					"script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; "+
					"form-action 'none'; frame-ancestors 'none'")
		}
		next.ServeHTTP(w, r)
	})
}

// hostCheck rejects requests whose Host header is not explicitly allowed. This
// blocks DNS-rebinding style attacks against a loopback listener.
func (s *Server) hostCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if !s.hostAllowed(host) {
			writeError(w, http.StatusMisdirectedRequest, "host_not_allowed",
				"this Host is not in allowed_hosts; add it to config if intentional")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		// Never log bodies, tokens or file contents.
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(),
			"ip", s.clientIP(r),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack is required by the WebSocket upgrade path.
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	conn, rw, err := h.Hijack()
	if err == nil {
		r.wrote = true
	}
	return conn, rw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// withAuth requires a valid paired-device token, presented as the session
// cookie or as an Authorization Bearer token, and rate-limits per device.
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rs, _, ok := s.authenticate(r)
		if !ok {
			// Only drop cookies that were actually presented and rejected, so a
			// failed Bearer restore cannot clear another device's cookie.
			if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
				s.clearSessionCookies(w, r)
			}
			writeError(w, http.StatusUnauthorized, "unauthenticated", "pairing required")
			return
		}
		if !s.limiter.allow(rs.TokenHash) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
			return
		}
		next(w, r.WithContext(withSession(r.Context(), rs)))
	}
}

// withCSRF enforces the double-submit token plus same-origin checks on
// state-changing requests.
func (s *Server) withCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.originAllowed(r) {
			writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
			return
		}
		cookie, err := r.Cookie(csrfCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusForbidden, "csrf_missing", "missing CSRF token; reload the page")
			return
		}
		if r.Header.Get(csrfHeader) != cookie.Value {
			writeError(w, http.StatusForbidden, "csrf_invalid", "CSRF token mismatch")
			return
		}
		next(w, r)
	}
}

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients (curl, tests) send no Origin; the CSRF token
		// still has to be valid, so this is not a CSRF bypass.
		return true
	}
	if origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if !s.hostAllowed(host) {
		return false
	}
	// Also require the origin host to match the Host header, so a page loaded
	// from another allowed name cannot drive this one.
	reqHost := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}
	return strings.Trim(reqHost, "[]") == host
}

// ---------- helpers ----------

func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.isTrustedProxy(r) {
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
			return strings.EqualFold(strings.TrimSpace(strings.Split(p, ",")[0]), "https")
		}
	}
	return false
}

func (s *Server) isTrustedProxy(r *http.Request) bool {
	if len(s.trusted) == 0 {
		return false
	}
	ip := net.ParseIP(s.peerIP(r))
	if ip == nil {
		return false
	}
	for _, n := range s.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) clientIP(r *http.Request) string {
	ip := s.peerIP(r)
	if !s.isTrustedProxy(r) {
		return ip
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if net.ParseIP(first) != nil {
			return first
		}
	}
	return ip
}

// cookieSecure decides the Secure attribute: an explicit config opt-in, or any
// request we know arrived over TLS (directly or from a trusted proxy). A plain
// HTTP request from an untrusted peer can never make a cookie Secure.
func (s *Server) cookieSecure(r *http.Request) bool {
	return s.cfg.CookieSecure || s.isHTTPS(r)
}

func (s *Server) setSessionCookies(w http.ResponseWriter, r *http.Request, token, csrf string, expires time.Time) {
	secure := s.cookieSecure(r)
	maxAge := int(time.Until(expires).Seconds())
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Expires:  expires,
	})
	// Readable by JS for the double-submit header; SameSite=Strict blocks
	// cross-site sends.
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    csrf,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
		Expires:  expires,
	})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter, r *http.Request) {
	secure := s.cookieSecure(r)
	for _, name := range []string{sessionCookie, csrfCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			HttpOnly: name == sessionCookie,
			Secure:   secure,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": msg},
	})
}

func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing data")
	}
	return nil
}

// uiHandler serves the embedded SPA with an index.html fallback for client
// routes, or a helpful placeholder when the frontend has not been built.
func (s *Server) uiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Unknown API paths must not fall through to the SPA (which would
		// answer 200 text/html for a typo'd endpoint).
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if s.ui == nil {
			s.serveUIPlaceholder(w)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			name = "index.html"
		}
		if st, err := fs.Stat(s.ui, name); err == nil && !st.IsDir() {
			http.ServeFileFS(w, r, s.ui, name)
			return
		}
		if _, err := fs.Stat(s.ui, "index.html"); err != nil {
			s.serveUIPlaceholder(w)
			return
		}
		http.ServeFileFS(w, r, s.ui, "index.html")
	})
}

func (s *Server) serveUIPlaceholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><title>FlowDrop</title>
<body style="font:16px system-ui;max-width:40rem;margin:4rem auto;padding:0 1rem">
<h1>FlowDrop Hub 正在运行</h1>
<p>前端资源尚未构建。生产构建：<code>scripts/build.sh</code>；开发模式：<code>scripts/dev.sh</code>（Vite 端口）。</p>
<p>健康检查：<a href="/api/v1/health">/api/v1/health</a></p></body>`)
}
