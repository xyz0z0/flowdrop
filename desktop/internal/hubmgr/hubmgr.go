// Package hubmgr owns the single in-process FlowDrop Hub that the desktop shell
// starts and stops. It is a thin lifecycle wrapper: all HTTP, auth, storage and
// discovery behaviour comes from flowdrop/pkg/hub, never from a copy here.
package hubmgr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"flowdrop/pkg/hub"
)

// Config is the subset of Hub options the desktop shell controls.
type Config struct {
	// ConfigPath is the Hub JSON config file (may not exist).
	ConfigPath string
	// DataDir is the Hub data directory (database + attachments + lock).
	DataDir string
	// BindAddress is a concrete local IPv4, never a wildcard. In dual mode it
	// is the primary address (the LAN one), whose URL Hub.URL() reports.
	BindAddress string
	// AdditionalBindAddresses are extra concrete local IPv4 addresses bound by
	// the same Hub on the same port (dual mode stores the Tailscale address).
	AdditionalBindAddresses []string
	// Port is the HTTP port, shared by every bound address.
	Port int
}

// Listen returns host:port for the primary address.
func (c Config) Listen() string {
	return net.JoinHostPort(c.BindAddress, fmt.Sprint(c.Port))
}

// AdditionalListen returns the extra host:port listeners. The result is always
// non-nil so the Hub's "nil inherits config file" semantics can never add an
// operator-configured listener behind the desktop's back: the desktop owns the
// complete set of listens it asks for.
func (c Config) AdditionalListen() []string {
	out := make([]string, 0, len(c.AdditionalBindAddresses))
	for _, addr := range c.AdditionalBindAddresses {
		out = append(out, net.JoinHostPort(addr, fmt.Sprint(c.Port)))
	}
	return out
}

// Status is a snapshot for the settings window.
type Status struct {
	Running     bool   `json:"running"`
	URL         string `json:"url"`
	Listen      string `json:"listen"`
	BindAddress string `json:"bind_address"`
	Port        int    `json:"port"`
	// URLs are the Hub's actual bound URLs (primary first). It is empty when
	// the Hub is not running; configured addresses are never reported here.
	URLs []string `json:"urls"`
	// AdditionalAddresses are the configured extra IPv4 addresses after the
	// last successful start (not a claim that they are currently bound).
	AdditionalAddresses []string `json:"additional_addresses"`
	// LastError is the last start/stop failure shown to the user, or "".
	LastError string `json:"last_error"`
	// AdminConfigured reports whether a Hub admin password exists.
	AdminConfigured bool `json:"admin_configured"`
}

// Manager serialises Hub start/stop/restart. Exactly one Hub instance exists at
// a time; a restart stops the old listener before opening the new one so the
// same data directory lock is released and re-acquired cleanly.
type Manager struct {
	mu      sync.Mutex
	cfg     Config
	hub     *hub.Hub
	lastErr string
	// lifetime is the Hub's context: Hub.Start treats it as the Hub lifetime
	// and shuts down when it is cancelled. The manager owns it so a caller's
	// short-lived startup context can never stop a healthy Hub later.
	lifetime context.Context
	cancel   context.CancelFunc
}

// New returns an idle manager.
func New() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{lifetime: ctx, cancel: cancel}
}

// Start creates and binds a Hub using cfg. It fails if a Hub is already
// running; callers that want to change the address use Restart.
func (m *Manager) Start(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hub != nil {
		return errors.New("hub is already running; stop or restart it instead")
	}
	// A previous Stop cancels the lifetime context; give the next instance a
	// fresh one so a later Start is not rejected by an already-cancelled ctx.
	if m.lifetime == nil || m.lifetime.Err() != nil {
		m.lifetime, m.cancel = context.WithCancel(context.Background())
	}
	return m.startLocked(cfg)
}

// startLocked assumes m.mu is held.
func (m *Manager) startLocked(cfg Config) error {
	if err := validate(cfg); err != nil {
		m.lastErr = err.Error()
		return err
	}
	h, err := hub.New(hub.Options{
		ConfigPath: cfg.ConfigPath,
		DataDir:    cfg.DataDir,
		Listen:     cfg.Listen(),
		// Always non-nil: the desktop owns the complete listen set, so an
		// additional_listen from an operator config file is never silently
		// added to (or inherited by) the desktop Hub.
		AdditionalListen: cfg.AdditionalListen(),
		// The desktop window talks plain HTTP to this Hub, so the session
		// cookie must not carry Secure; the loopback/LAN boundary is what
		// protects it, exactly as documented for the CLI LAN mode.
		CookieSecure: boolPtr(false),
		// Leave discovery at the Hub's automatic default: it still refuses to
		// advertise loopback, Tailscale or wildcard binds.
		DiscoveryEnabled: nil,
	})
	if err != nil {
		m.lastErr = err.Error()
		return err
	}
	if err := h.Start(m.lifetime); err != nil {
		m.lastErr = err.Error()
		// Release the data-dir lock and database acquired by New.
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = h.Stop(stopCtx)
		cancel()
		return err
	}
	m.hub = h
	m.cfg = cfg
	m.lastErr = ""
	return nil
}

// Stop shuts the Hub down (mDNS withdrawn, port released) and is safe to call
// when nothing is running.
func (m *Manager) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stopLocked(ctx)
}

func (m *Manager) stopLocked(ctx context.Context) error {
	if m.hub == nil {
		return nil
	}
	err := m.hub.Stop(ctx)
	if m.cancel != nil {
		m.cancel()
	}
	m.hub = nil
	if err != nil {
		m.lastErr = err.Error()
	}
	return err
}

// Restart stops the current Hub and starts a new one with cfg against the same
// data directory. If the new address cannot be bound the old Hub is gone, so
// the error is reported and the caller can offer to retry or pick another
// address; the data directory is never silently reused by a second process.
func (m *Manager) Restart(ctx context.Context, cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.stopLocked(ctx); err != nil {
		return fmt.Errorf("stop previous hub: %w", err)
	}
	// A stopped Hub cancels its lifetime context; give the next instance a
	// fresh one so a restart is not immediately torn down.
	m.lifetime, m.cancel = context.WithCancel(context.Background())
	return m.startLocked(cfg)
}

// URL returns the running Hub's base URL, or "".
func (m *Manager) URL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hub == nil {
		return ""
	}
	return m.hub.URL()
}

// URLs returns every actual base URL of the running Hub (primary first), or an
// empty slice when nothing is bound.
func (m *Manager) URLs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hub == nil {
		return []string{}
	}
	urls := m.hub.URLs()
	if urls == nil {
		return []string{}
	}
	return urls
}

// Handler exposes the Hub HTTP handler (used by tests; the desktop window talks
// to the real listener).
func (m *Manager) Handler() (*hub.Hub, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hub, m.hub != nil
}

// HasAdmin reports whether an admin password exists.
func (m *Manager) HasAdmin() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hub == nil {
		return false, errors.New("hub is not running")
	}
	return m.hub.HasAdmin()
}

// InitializeAdmin creates the first admin password. It refuses to overwrite an
// existing one (the Hub enforces that too).
func (m *Manager) InitializeAdmin(username, password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hub == nil {
		return errors.New("hub is not running")
	}
	return m.hub.InitializeAdmin(username, password)
}

// Status snapshots the manager for the settings window.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{
		LastError:           m.lastErr,
		BindAddress:         m.cfg.BindAddress,
		Port:                m.cfg.Port,
		Listen:              m.cfg.Listen(),
		AdditionalAddresses: append([]string(nil), m.cfg.AdditionalBindAddresses...),
	}
	if m.hub != nil {
		st.Running = true
		st.URL = m.hub.URL()
		st.URLs = m.URLsLocked()
		if ok, err := m.hub.HasAdmin(); err == nil {
			st.AdminConfigured = ok
		}
	}
	return st
}

// URLsLocked returns the actual URLs; m.mu must be held.
func (m *Manager) URLsLocked() []string {
	if m.hub == nil {
		return []string{}
	}
	urls := m.hub.URLs()
	if urls == nil {
		return []string{}
	}
	return urls
}

// Config returns the configuration of the last successful start.
func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func validate(cfg Config) error {
	if err := validateAddress(cfg.BindAddress); err != nil {
		return err
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port %d is out of range 1-65535", cfg.Port)
	}
	if len(cfg.AdditionalBindAddresses) > 1 {
		return fmt.Errorf("at most one additional bind address is supported, got %d", len(cfg.AdditionalBindAddresses))
	}
	seen := map[string]bool{cfg.BindAddress: true}
	for _, addr := range cfg.AdditionalBindAddresses {
		if err := validateAddress(addr); err != nil {
			return fmt.Errorf("additional bind address: %w", err)
		}
		if seen[addr] {
			return fmt.Errorf("additional bind address %q is duplicated; each listener needs its own address", addr)
		}
		seen[addr] = true
	}
	return nil
}

func validateAddress(addr string) error {
	ip := net.ParseIP(addr)
	if ip == nil {
		return fmt.Errorf("bind address %q is not an IP address", addr)
	}
	if ip.To4() == nil {
		return fmt.Errorf("bind address %q is not IPv4 (only IPv4 is supported)", addr)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("bind address %q is a wildcard; pick the concrete LAN or Tailscale IP", addr)
	}
	return nil
}

func boolPtr(v bool) *bool { return &v }

// FriendlyError turns a Hub start failure into a message a non-technical user
// can act on. It never suggests killing a process that belongs to someone else.
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case errors.Is(err, hub.ErrDataDirInUse):
		return "数据目录已被另一个 FlowDrop 使用（命令行 Hub 或另一个桌面实例）。\n" +
			"请先关闭那个进程，或在设置中选择其他数据目录后重试。本应用不会结束它。"
	case strings.Contains(msg, "address already in use"):
		return "端口已被占用（可能是另一个 FlowDrop 或其他程序）。\n" +
			"请在设置中换一个端口或先关闭占用者；本应用不会结束其他进程。"
	case strings.Contains(msg, "cannot assign requested address"):
		return "本机当前没有这个 IP 地址。\n" +
			"可能是网络切换或 IP 变化，请在设置中重新选择绑定地址。"
	case strings.Contains(msg, "permission denied"):
		return "没有权限绑定该端口。\n小于 1024 的端口通常需要管理员权限，请改用 1024 以上的端口。"
	}
	return msg
}
