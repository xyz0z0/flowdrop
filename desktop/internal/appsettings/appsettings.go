// Package appsettings stores the desktop shell's own small preferences:
// the bind address and port of the embedded Hub. It deliberately contains no
// password, session token or Hub data — those stay in the Hub (SQLite) and the
// Hub config file.
package appsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// Settings is the persisted desktop configuration. Only the values the user
// picks in the graphical settings window live here.
type Settings struct {
	// BindAddress is one concrete local IPv4 address (never 0.0.0.0 / ::).
	// In dual mode it is the primary address, which the inbox window prefers.
	BindAddress string `json:"bind_address"`
	// Port is the Hub HTTP port, shared by every bound address.
	Port int `json:"port"`
	// AdditionalBindAddresses are extra concrete local IPv4 addresses bound by
	// the same Hub on the same port. The desktop's dual mode stores exactly
	// one: the Tailscale address, while BindAddress is the LAN address. It is
	// omitted for the legacy single-address mode, so desktop.json files written
	// by older versions keep loading unchanged.
	AdditionalBindAddresses []string `json:"additional_bind_addresses,omitempty"`
}

// Default returns the starting point used before the user ever saved settings:
// loopback and 8080. The caller normally replaces BindAddress with a detected
// LAN or Tailscale address.
func Default() Settings {
	return Settings{BindAddress: "127.0.0.1", Port: 8080}
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
	if err := settings.Validate(); err != nil {
		return settings, true, fmt.Errorf("desktop settings %s: %w", path, err)
	}
	return settings, true, nil
}

// Save writes the settings atomically with 0600 permissions.
func Save(path string, s Settings) error {
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
