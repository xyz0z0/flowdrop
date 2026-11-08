// Package config loads FlowDrop Hub configuration from defaults, an optional
// JSON file, and FLOWDROP_* environment variables (env wins).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Listen is the HTTP listen address. Keep it loopback-only when a TLS
	// terminating reverse proxy (Tailscale Serve / Caddy) fronts it. For the
	// accepted plaintext-HTTP LAN mode, bind the specific LAN IP (never 0.0.0.0)
	// and set cookie_secure=false; see deploy/http-lan.env.example.
	Listen string `json:"listen"`
	// AdditionalListen lists extra explicit host:port endpoints bound by the
	// same Hub instance. Every entry must name a literal, non-wildcard IP;
	// hostnames and 0.0.0.0/:: are deliberately rejected so a typo can never
	// expose plaintext HTTP on an unintended interface. The primary Listen is
	// always bound first and remains the address reported by URL(). Same-port
	// different-IP combinations are allowed; exact duplicates are ignored.
	// Port 0 asks the OS for a port: every :0 listener gets its own ephemeral
	// port and hub.URLs() reports the actual ones.
	AdditionalListen []string `json:"additional_listen"`
	// DataDir holds flowdrop.db, blobs/ and tmp/.
	DataDir string `json:"data_dir"`
	// AllowedHosts is an explicit allowlist of Host header hostnames.
	// Loopback names are always allowed. Empty means loopback only.
	AllowedHosts []string `json:"allowed_hosts"`
	// TrustedProxies is a list of IPs or CIDRs whose X-Forwarded-* headers are
	// honored. Empty means no proxy headers are trusted.
	TrustedProxies []string `json:"trusted_proxies"`

	MaxUploadBytes       int64 `json:"max_upload_bytes"`
	MaxConcurrentUploads int   `json:"max_concurrent_uploads"`
	MinFreeBytes         int64 `json:"min_free_bytes"`

	SessionTTLHours int `json:"session_ttl_hours"`
	// CookieSecure defaults to true (HTTPS only). It must be false for the
	// plaintext-HTTP trusted-LAN mode, otherwise browsers drop the cookies and
	// login appears to succeed while every follow-up request is 401.
	CookieSecure bool `json:"cookie_secure"`

	// DiscoveryEnabled controls the mDNS/DNS-SD LAN announcement. nil means
	// "automatic": publish only the specific RFC1918 IPv4 listeners bound with
	// cookie_secure=false. Set false to switch discovery off explicitly.
	// discovery_enabled=true never upgrades loopback, Tailscale, wildcard or
	// HTTPS-only listeners into a LAN broadcast; it only makes the intent
	// explicit.
	DiscoveryEnabled *bool `json:"discovery_enabled"`

	LoginLimitPerMinute int `json:"login_limit_per_minute"`
	APILimitPerMinute   int `json:"api_limit_per_minute"`
}

func Default() Config {
	return Config{
		Listen:               "127.0.0.1:8080",
		AdditionalListen:     nil,
		DataDir:              defaultDataDir(),
		AllowedHosts:         nil,
		TrustedProxies:       nil,
		MaxUploadBytes:       512 << 20,
		MaxConcurrentUploads: 4,
		MinFreeBytes:         1 << 30,
		SessionTTLHours:      168,
		CookieSecure:         true,
		LoginLimitPerMinute:  5,
		APILimitPerMinute:    600,
	}
}

func defaultDataDir() string {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "flowdrop")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "data"
	}
	return filepath.Join(home, ".local", "share", "flowdrop")
}

// Load reads defaults, then the given JSON file if it exists, then env vars.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// Fine: defaults + env only.
		default:
			return cfg, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	if err := cfg.applyEnv(); err != nil {
		return cfg, err
	}
	return cfg, cfg.Validate()
}

func (c *Config) applyEnv() error {
	setStr := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	setInt := func(key string, dst *int) error {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = n
		}
		return nil
	}
	setInt64 := func(key string, dst *int64) error {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = n
		}
		return nil
	}
	setBool := func(key string, dst *bool) error {
		if v, ok := os.LookupEnv(key); ok {
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = b
		}
		return nil
	}
	setList := func(key string, dst *[]string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = splitList(v)
		}
	}

	setStr("FLOWDROP_LISTEN", &c.Listen)
	setList("FLOWDROP_ADDITIONAL_LISTEN", &c.AdditionalListen)
	setStr("FLOWDROP_DATA_DIR", &c.DataDir)
	setList("FLOWDROP_ALLOWED_HOSTS", &c.AllowedHosts)
	setList("FLOWDROP_TRUSTED_PROXIES", &c.TrustedProxies)
	if err := setInt64("FLOWDROP_MAX_UPLOAD_BYTES", &c.MaxUploadBytes); err != nil {
		return err
	}
	if err := setInt("FLOWDROP_MAX_CONCURRENT_UPLOADS", &c.MaxConcurrentUploads); err != nil {
		return err
	}
	if err := setInt64("FLOWDROP_MIN_FREE_BYTES", &c.MinFreeBytes); err != nil {
		return err
	}
	if err := setInt("FLOWDROP_SESSION_TTL_HOURS", &c.SessionTTLHours); err != nil {
		return err
	}
	if err := setBool("FLOWDROP_COOKIE_SECURE", &c.CookieSecure); err != nil {
		return err
	}
	if v, ok := os.LookupEnv("FLOWDROP_DISCOVERY_ENABLED"); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("FLOWDROP_DISCOVERY_ENABLED: %w", err)
		}
		c.DiscoveryEnabled = &b
	}
	if err := setInt("FLOWDROP_LOGIN_LIMIT_PER_MINUTE", &c.LoginLimitPerMinute); err != nil {
		return err
	}
	if err := setInt("FLOWDROP_API_LIMIT_PER_MINUTE", &c.APILimitPerMinute); err != nil {
		return err
	}
	return nil
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *Config) Validate() error {
	if c.Listen == "" {
		return errors.New("listen address is empty")
	}
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen must be host:port: %w", err)
	}
	for i, addr := range c.AdditionalListen {
		if err := validateAdditionalListen(addr); err != nil {
			return fmt.Errorf("additional_listen[%d] %q: %w", i, addr, err)
		}
	}
	if !c.CookieSecure {
		// The rule covers every listener, not just the primary address: a
		// wildcard anywhere would expose plaintext HTTP on all interfaces.
		for _, addr := range c.Listens() {
			if IsWildcardListen(addr) {
				return errors.New("listen must name the specific LAN IP when cookie_secure=false; " +
					"binding 0.0.0.0/:: would expose plaintext HTTP on every interface")
			}
		}
	}
	if c.DataDir == "" {
		return errors.New("data_dir is empty")
	}
	if c.MaxUploadBytes <= 0 {
		return errors.New("max_upload_bytes must be positive")
	}
	if c.MaxConcurrentUploads <= 0 {
		return errors.New("max_concurrent_uploads must be positive")
	}
	if c.MinFreeBytes < 0 {
		return errors.New("min_free_bytes must not be negative")
	}
	if c.SessionTTLHours <= 0 {
		return errors.New("session_ttl_hours must be positive")
	}
	if c.LoginLimitPerMinute <= 0 {
		c.LoginLimitPerMinute = 5
	}
	if c.APILimitPerMinute <= 0 {
		c.APILimitPerMinute = 600
	}
	for _, p := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(p); err != nil {
			if net.ParseIP(p) == nil {
				return fmt.Errorf("trusted_proxies entry %q is not an IP or CIDR", p)
			}
		}
	}
	for _, h := range c.AllowedHosts {
		if strings.ContainsAny(h, "/ ") {
			return fmt.Errorf("allowed_hosts entry %q contains invalid characters", h)
		}
	}
	return nil
}

func (c Config) SessionTTL() time.Duration {
	return time.Duration(c.SessionTTLHours) * time.Hour
}

// DiscoveryOn reports whether LAN discovery is enabled (the default when the
// config key is absent). It does not imply that the current listen address is
// eligible; the discovery package makes that decision.
func (c Config) DiscoveryOn() bool {
	return c.DiscoveryEnabled == nil || *c.DiscoveryEnabled
}

// DiscoveryExplicit reports whether discovery_enabled was set by the operator.
func (c Config) DiscoveryExplicit() bool {
	return c.DiscoveryEnabled != nil
}

// ListenIsLoopback reports whether the primary listen address is loopback-only.
func (c Config) ListenIsLoopback() bool {
	return IsLoopbackListen(c.Listen)
}

// ListenIsWildcard reports whether the primary listen address binds every
// interface (0.0.0.0, ::, or an empty host). Such a bind is refused when
// cookies are not Secure so plaintext HTTP never silently covers every NIC.
func (c Config) ListenIsWildcard() bool {
	return IsWildcardListen(c.Listen)
}

// AllListensLoopback reports whether every configured listener is loopback.
func (c Config) AllListensLoopback() bool {
	for _, addr := range c.Listens() {
		if !IsLoopbackListen(addr) {
			return false
		}
	}
	return true
}

// Listens returns the primary listen address followed by the additional
// explicit addresses. Exact duplicates (after IP/port canonicalisation) are
// dropped while order is preserved, so the primary address always stays first.
// Same-port/different-IP combinations are distinct listeners and are kept.
func (c Config) Listens() []string {
	out := make([]string, 0, 1+len(c.AdditionalListen))
	seen := make(map[string]struct{}, 1+len(c.AdditionalListen))
	for _, addr := range append([]string{c.Listen}, c.AdditionalListen...) {
		key := canonicalListen(addr)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, addr)
	}
	return out
}

// IsLoopbackListen reports whether one host:port names a loopback IP.
func IsLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// IsWildcardListen reports whether one host:port binds every interface.
func IsWildcardListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

func validateAdditionalListen(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	if host == "" {
		return errors.New("must name a specific IP, not a wildcard")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("must name a literal IP, not hostname %q", host)
	}
	if ip.IsUnspecified() {
		return errors.New("must name a specific IP, not a wildcard")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return fmt.Errorf("port %q is invalid", portStr)
	}
	return nil
}

// canonicalListen normalises an address for duplicate detection only. It does
// not replace the operator-facing string returned by Listens.
func canonicalListen(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(addr))
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	if n, err := strconv.Atoi(port); err == nil {
		port = strconv.Itoa(n)
	}
	return net.JoinHostPort(host, port)
}
