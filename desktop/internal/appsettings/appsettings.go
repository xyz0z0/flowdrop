// Package appsettings stores the desktop shell's own small preferences:
// the bind address and port of the embedded Hub plus, once the shell has
// paired itself as a native Hub client, the device ID and device token that
// let it reopen the inbox without a manual pairing code. It never contains a
// password or a browser session cookie — those stay in the Hub (SQLite) and
// the Hub config file.
package appsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Run modes of the desktop shell.
//
// ModeLocal starts the in-process Hub (the historical behaviour). ModeRemote
// starts no Hub at all and points the inbox window at an existing Hub on
// another machine, for example the Linux box running a CLI Hub.
const (
	ModeLocal  = "local"
	ModeRemote = "remote"
)

// Settings is the persisted desktop configuration. Only the values the user
// picks in the graphical settings window live here.
type Settings struct {
	// Mode is the run mode: ModeLocal starts the local Hub, ModeRemote connects
	// to an existing Hub. It is required and always written; a file without the
	// field is unusable and Load rejects it instead of guessing a mode.
	Mode string `json:"mode"`
	// RemoteURL is the base URL of the remote Hub (for example
	// http://192.168.31.184:8080). It is only kept in remote mode and is always
	// stored without a trailing slash.
	RemoteURL string `json:"remote_url,omitempty"`
	// BindAddress is one concrete local IPv4 address (never 0.0.0.0 / ::).
	// In dual mode it is the primary address, which the inbox window prefers.
	BindAddress string `json:"bind_address"`
	// Port is the Hub HTTP port, shared by every bound address.
	Port int `json:"port"`
	// AdditionalBindAddresses are extra concrete local IPv4 addresses bound by
	// the same Hub on the same port. The desktop's dual mode stores exactly
	// one: the Tailscale address, while BindAddress is the LAN address. Single
	// address mode writes no such field at all.
	AdditionalBindAddresses []string `json:"additional_bind_addresses,omitempty"`
	// DesktopDeviceID is the Hub's record ID for this desktop, learned when the
	// shell paired itself as a native client. It is not a credential: it is only
	// used to notice that the saved device was revoked or replaced.
	DesktopDeviceID string `json:"desktop_device_id"`
	// DesktopDeviceToken is the long-lived native device token the shell got
	// from POST /api/v1/auth/pair with native=true. It is the one credential in
	// this file (hence the 0600 atomic write) and only valid in the base64url raw
	// 32-byte form the Hub generates; anything else is dropped on load and save,
	// so a corrupted file simply pairs again.
	DesktopDeviceToken string `json:"desktop_device_token"`
}

// Accepted shapes for the device identity above. Both come from the Hub
// (server/internal/pairing/pairing.go): 32 random bytes base64url-encoded
// without padding for the token, and its own opaque device ID.
var (
	deviceTokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	deviceIDRE    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

// Default returns the starting point used before the user ever saved settings:
// local mode on loopback and 8080. The caller normally replaces BindAddress
// with a detected LAN or Tailscale address.
func Default() Settings {
	return Settings{Mode: ModeLocal, BindAddress: "127.0.0.1", Port: 8080}
}

// IsRemote reports whether the desktop shell should connect to an existing Hub
// instead of starting its own.
func (s Settings) IsRemote() bool { return s.Mode == ModeRemote }

// Normalize applies the cross-field rules so a stored file never carries a
// contradictory combination: a device identity the Hub cannot have issued is
// dropped, a remote URL is only meaningful in remote mode (it is dropped
// otherwise, so a stale address cannot hijack a later start), and a remote URL
// never keeps the trailing slash the inbox window would double up on.
func (s Settings) Normalize() Settings {
	// A device identity the Hub cannot have issued is indistinguishable from no
	// identity at all: drop it so the shell pairs again instead of sending a
	// malformed token. The rest of the file is untouched.
	if !deviceIDRE.MatchString(s.DesktopDeviceID) {
		s.DesktopDeviceID = ""
	}
	if !deviceTokenRE.MatchString(s.DesktopDeviceToken) {
		s.DesktopDeviceToken = ""
	}
	if s.Mode != ModeRemote {
		s.RemoteURL = ""
		return s
	}
	s.RemoteURL = strings.TrimRight(strings.TrimSpace(s.RemoteURL), "/")
	return s
}

// DualMode reports whether the user asked for more than one bound address.
func (s Settings) DualMode() bool { return len(s.AdditionalBindAddresses) > 0 }

// AllBindAddresses returns the primary address followed by the additional ones
// in configured order.
func (s Settings) AllBindAddresses() []string {
	out := make([]string, 0, 1+len(s.AdditionalBindAddresses))
	out = append(out, s.BindAddress)
	out = append(out, s.AdditionalBindAddresses...)
	return out
}

// AdditionalListen returns the additional listeners (ip:port) for the Hub.
// The result is always non-nil: the Hub treats nil as "inherit the config
// file", and the desktop always decides the complete listen set itself (an
// empty slice explicitly means single-address mode).
func (s Settings) AdditionalListen() []string {
	out := make([]string, 0, len(s.AdditionalBindAddresses))
	for _, addr := range s.AdditionalBindAddresses {
		out = append(out, net.JoinHostPort(addr, strconv.Itoa(s.Port)))
	}
	return out
}

// DefaultPath resolves the per-user desktop settings file:
// FLOWDROP_DESKTOP_CONFIG, then XDG_CONFIG_HOME/flowdrop/desktop.json, then
// ~/.config/flowdrop/desktop.json.
func DefaultPath() string {
	if v := os.Getenv("FLOWDROP_DESKTOP_CONFIG"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "flowdrop", "desktop.json")
	}
	// os.UserConfigDir is exactly $XDG_CONFIG_HOME or ~/.config on Linux, so
	// this matches the manual join below, and %AppData% on Windows.
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "flowdrop", "desktop.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "desktop.json"
	}
	return filepath.Join(home, ".config", "flowdrop", "desktop.json")
}

// Load reads the settings file. A missing file is not an error: it returns the
// zero Settings and found=false so the caller can apply its own defaults. The
// returned settings are validated.
func Load(path string) (settings Settings, found bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{}, false, nil
	}
	if err != nil {
		return Settings{}, false, fmt.Errorf("read desktop settings %s: %w", path, err)
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return Settings{}, true, fmt.Errorf("parse desktop settings %s: %w", path, err)
	}
	// Normalize never invents a mode: a file without one fails Validate below,
	// so the caller has to write a file it can use instead of guessing.
	settings = settings.Normalize()
	if err := settings.Validate(); err != nil {
		return settings, true, fmt.Errorf("desktop settings %s: %w", path, err)
	}
	return settings, true, nil
}

// Save writes the settings atomically with 0600 permissions. Cross-field rules
// are applied first, so a non-remote mode never persists a leftover remote URL.
func Save(path string, s Settings) error {
	s = s.Normalize()
	if err := s.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create settings dir: %w", err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".desktop-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Validate rejects values the Hub or the desktop window cannot use: a wildcard
// bind would expose every interface, and a non-IPv4 bind is out of scope for
// this Linux desktop milestone. Additional addresses must describe a real
// second listener: distinct from the primary and from each other. The product
// mode is exactly LAN + Tailscale, so at most one additional address is
// accepted.
func (s Settings) Validate() error {
	if err := s.validateMode(); err != nil {
		return err
	}
	if err := ValidateBindAddress(s.BindAddress); err != nil {
		return err
	}
	if err := ValidatePort(s.Port); err != nil {
		return err
	}
	if len(s.AdditionalBindAddresses) > 1 {
		return fmt.Errorf("additional bind addresses: at most one extra address is supported (dual mode is LAN + Tailscale), got %d", len(s.AdditionalBindAddresses))
	}
	seen := map[string]bool{s.BindAddress: true}
	for _, addr := range s.AdditionalBindAddresses {
		if err := ValidateBindAddress(addr); err != nil {
			return fmt.Errorf("additional bind address: %w", err)
		}
		if seen[addr] {
			return fmt.Errorf("additional bind address %q is duplicated; the two listeners must be different addresses", addr)
		}
		seen[addr] = true
	}
	return nil
}

// validateMode accepts only the two known modes and, in remote mode, a usable
// Hub base URL. The URL rules matter because it becomes the inbox window's
// origin: a path, query, userinfo or fragment would either change the page the
// window shows or send the credentials elsewhere.
func (s Settings) validateMode() error {
	switch s.Mode {
	case ModeLocal:
		return nil
	case ModeRemote:
		return ValidateRemoteURL(s.RemoteURL)
	default:
		// An empty mode is the "file without a mode field" case: it is unusable
		// rather than silently treated as local.
		return fmt.Errorf("运行模式 %q 无效；必须填写本机 Hub（%q）或连接远程 Hub（%q）", s.Mode, ModeLocal, ModeRemote)
	}
}

// ValidateRemoteURL requires a bare http(s) base URL with a host: an origin the
// inbox window can load and whose own pairing page it can use.
func ValidateRemoteURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("远程 Hub 模式需要填写 Hub 地址，例如 http://192.168.31.184:8080")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("远程 Hub 地址 %q 无法解析，请填写形如 http://192.168.31.184:8080 的地址：%w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("远程 Hub 地址 %q 必须以 http:// 或 https:// 开头", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("远程 Hub 地址 %q 缺少主机名和端口", raw)
	}
	if u.User != nil {
		return fmt.Errorf("远程 Hub 地址 %q 不能包含用户名或密码", raw)
	}
	if u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("远程 Hub 地址 %q 不能包含查询参数（? 之后的内容）", raw)
	}
	if u.Fragment != "" {
		return fmt.Errorf("远程 Hub 地址 %q 不能包含 # 片段", raw)
	}
	if path := strings.TrimRight(u.Path, "/"); path != "" {
		return fmt.Errorf("远程 Hub 地址 %q 只能写到主机和端口，不能带路径", raw)
	}
	return nil
}

// ValidateBindAddress requires a concrete local IPv4 address. Wildcard binds
// are refused so the desktop never silently listens on every interface.
func ValidateBindAddress(addr string) error {
	if addr == "" {
		return errors.New("bind address is empty")
	}
	ip := net.ParseIP(addr)
	if ip == nil {
		return fmt.Errorf("bind address %q is not an IP address", addr)
	}
	if ip.To4() == nil {
		return fmt.Errorf("bind address %q is not IPv4 (only IPv4 is supported in this milestone)", addr)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("bind address %q is a wildcard; choose the concrete LAN or Tailscale IP", addr)
	}
	return nil
}

// ValidatePort accepts a usable TCP port.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of range 1-65535", port)
	}
	return nil
}

// Listen returns the host:port the Hub should bind.
func (s Settings) Listen() string {
	return net.JoinHostPort(s.BindAddress, strconv.Itoa(s.Port))
}
