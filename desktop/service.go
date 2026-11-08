package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/desktop/internal/hubmgr"
	"flowdrop/desktop/internal/network"
	"flowdrop/pkg/hub"
)

// SettingsService is the Wails-bound API of the settings window. It is only
// reachable from the in-process asset server origin (wails://localhost); the
// Hub-origin inbox window cannot call any of these methods.
type SettingsService struct {
	manager *hubmgr.Manager
	app     *application.App // set once the application exists

}

// serviceSingleton lets main() hand the created app to the bound instance.
var (
	serviceMu    sync.Mutex
	serviceValue *SettingsService
)

func newSettingsService(m *hubmgr.Manager) *SettingsService {
	s := &SettingsService{manager: m}
	serviceMu.Lock()
	serviceValue = s
	serviceMu.Unlock()
	return s
}

func syncServiceApp(app *application.App) {
	serviceMu.Lock()
	defer serviceMu.Unlock()
	if serviceValue != nil {
		serviceValue.app = app
	}
}

// InterfaceOption is one selectable bind address.
type InterfaceOption struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended"`
}

// State is everything the settings window renders.
type State struct {
	Running       bool              `json:"running"`
	URL           string            `json:"url"`
	URLs          []string          `json:"urls"`
	AccessURLs    []AccessURL       `json:"access_urls"`
	BindAddress   string            `json:"bind_address"`
	Additional    []string          `json:"additional_addresses"`
	DualMode      bool              `json:"dual_mode"`
	DualAvailable bool              `json:"dual_available"`
	Port          int               `json:"port"`
	LastError     string            `json:"last_error"`
	StartupError  string            `json:"startup_error"`
	ConfigPath    string            `json:"config_path"`
	Interfaces    []InterfaceOption `json:"interfaces"`
	HasTailscale  bool              `json:"has_tailscale"`
	TrayAvailable bool              `json:"tray_available"`
	ProxyWarning  string            `json:"proxy_warning"`
	// AccessibilityWarning explains that this launch had to disable the
	// AT-SPI bridge because the session's accessibility bus was broken. It is
	// empty when accessibility is enabled.
	AccessibilityWarning string `json:"accessibility_warning"`
}

// GetState returns the current shell state for the settings window.
func (s *SettingsService) GetState() (State, error) {
	st := State{
		ConfigPath:    desktop.settingsPath,
		StartupError:  desktop.lastStartupErr,
		TrayAvailable: desktop.trayAvailable,
		URLs:          []string{},
		AccessURLs:    []AccessURL{},
		Additional:    []string{},
	}
	list, listErr := network.List()
	for _, i := range list {
		st.Interfaces = append(st.Interfaces, InterfaceOption{
			Name:        i.Name,
			Address:     i.Address,
			Kind:        string(i.Kind),
			Label:       i.Label,
			Recommended: i.Recommended,
		})
	}
	if st.Interfaces == nil {
		st.Interfaces = []InterfaceOption{}
	}
	st.HasTailscale = network.HasTailscale(list)
	st.DualAvailable = dualModeAvailable(list)

	if s.manager != nil {
		snap := s.manager.Status()
		st.Running = snap.Running
		st.URL = snap.URL
		st.URLs = append([]string{}, snap.URLs...)
		st.BindAddress = snap.BindAddress
		st.Additional = append([]string{}, snap.AdditionalAddresses...)
		st.Port = snap.Port
		if snap.LastError != "" {
			st.LastError = hubmgr.FriendlyError(errors.New(snap.LastError))
		}
	}
	// When the Hub failed to start there is no running config; show what the
	// user asked for so the failing address/port is visible and editable.
	if !st.Running && desktop.settingsPath != "" {
		if saved, found, err := appsettings.Load(desktop.settingsPath); err == nil && found {
			st.BindAddress = saved.BindAddress
			st.Port = saved.Port
			st.Additional = append([]string{}, saved.AdditionalBindAddresses...)
		}
	}
	st.DualMode = len(st.Additional) > 0
	// Only actual bound URLs are shown as usable. Configured addresses that are
	// not running remain visible as the requested inputs, never as access URLs.
	if st.Running {
		st.AccessURLs = describeAccessURLs(list, st.URLs)
	}
	if st.BindAddress != "" {
		host := st.BindAddress
		if inbox := preferredAccessURL(list, st.URLs); inbox != "" {
			if u, err := url.Parse(inbox); err == nil {
				host = u.Hostname()
			}
		}
		st.ProxyWarning = desktopProxyWarning(host)
	}
	st.AccessibilityWarning = desktopAccessibilityWarning(desktop.a11yFallbackReason())
	if listErr != nil && st.LastError == "" {
		st.LastError = listErr.Error()
	}
	return st, nil
}

// desktopAccessibilityWarning turns the recorded bridge-disable reason into a
// user-facing note. An empty reason means accessibility is on.
func desktopAccessibilityWarning(reason string) string {
	if reason == "" {
		return ""
	}
	return "本次启动检测到系统的 AT-SPI 无障碍总线无响应（" + reason + "），" +
		"为避免 at-spi/WebKitGTK 原生崩溃，已仅对本应用禁用无障碍桥；" +
		"屏幕阅读器本次无法读取 FlowDrop。系统无障碍设置没有改动；" +
		"注销并重新登录（或修复会话的无障碍总线）后重新启动即可恢复。" +
		"临时手动方式：NO_AT_BRIDGE=1 启动本应用；" +
		"FLOWDROP_DESKTOP_A11Y=on 可强制保留无障碍（有崩溃风险）。"
}

// desktopProxyWarning combines the host environment diagnosis with what this
// process actually managed to configure in WebKitGTK:
//
//   - in-app direct mode applied: the WebSocket no longer goes through the
//     system proxy, so the old "add this address to no_proxy" demand is gone;
//   - not applied and a proxy risk exists: keep the warning and state plainly
//     that the in-app bypass failed, instead of hiding the risk;
//   - no proxy risk detected: show nothing, including when the bypass failed
//     (there is nothing for it to bypass).
func desktopProxyWarning(host string) string {
	return combineProxyWarning(
		network.ProxyWarning(host),
		desktop.webviewProxyConfigured.Load(),
		desktop.webviewProxyConfigureError(),
	)
}

// combineProxyWarning is the pure composition used by desktopProxyWarning so
// the success/failure policy can be unit-tested without a live WebKit context.
func combineProxyWarning(environmentWarning string, webviewDirect bool, configureErr string) string {
	if environmentWarning == "" {
		return ""
	}
	if webviewDirect {
		return ""
	}
	if configureErr == "" {
		return environmentWarning
	}
	return "应用内 WebView 直连代理设置失败（" + configureErr + "）。\n" + environmentWarning
}

// a11yFallbackReason returns why the AT-SPI bridge was disabled for this
// launch, or "" when accessibility is enabled or has not been checked.
func (d *desktopState) a11yFallbackReason() string {
	if v, ok := d.a11yFallback.Load().(string); ok {
		return v
	}
	return ""
}

// webviewProxyConfigureError returns the in-app proxy setup error, or "" when
// it succeeded or has not run yet.
func (d *desktopState) webviewProxyConfigureError() string {
	if v, ok := d.webviewProxyConfigureErr.Load().(string); ok {
		return v
	}
	return ""
}

// ApplyNetwork keeps the original single-address API working: it binds one
// concrete IPv4 address/port.
func (s *SettingsService) ApplyNetwork(bindAddress string, port int) (State, error) {
	return s.applyNetwork(bindAddress, nil, port)
}

// ApplyNetworkConfig applies the complete listen selection: a primary address
// plus optional additional addresses (dual mode stores exactly one Tailscale
// address next to the LAN primary) on one shared port. The desktop bridge is
// the only caller; there is no network management endpoint.
func (s *SettingsService) ApplyNetworkConfig(bindAddress string, additional []string, port int) (State, error) {
	return s.applyNetwork(bindAddress, additional, port)
}

// ApplyDualNetwork is the settings window's explicit dual-mode entry point:
// one LAN address and one Tailscale address, both bound by the same Hub on the
// same port. An empty Tailscale address is rejected instead of quietly falling
// back to single mode.
func (s *SettingsService) ApplyDualNetwork(lanAddress, tailscaleAddress string, port int) (State, error) {
	if tailscaleAddress == "" {
		return s.mustState(), errors.New("双模式需要选择 Tailscale 地址；如果只想用一个地址，请切换到单地址模式")
	}
	return s.applyNetwork(lanAddress, []string{tailscaleAddress}, port)
}

// applyNetwork validates, restarts and persists a listen selection. It stops the
// old Hub and starts a new one against the same data directory, then reloads the
// inbox window onto the new URL. All listeners are bound by that one Hub/one
// database/one data-directory lock; a partially bound set is never reported as
// success. If the new selection cannot be bound, the previous configuration is
// started again so the app is not left without a Hub.
func (s *SettingsService) applyNetwork(bindAddress string, additional []string, port int) (State, error) {
	if err := appsettings.ValidateBindAddress(bindAddress); err != nil {
		return s.mustState(), err
	}
	if err := appsettings.ValidatePort(port); err != nil {
		return s.mustState(), err
	}
	for _, addr := range additional {
		if err := appsettings.ValidateBindAddress(addr); err != nil {
			return s.mustState(), fmt.Errorf("附加地址：%w", err)
		}
	}
	list, err := network.List()
	if err != nil {
		return s.mustState(), err
	}
	if bindAddress != "127.0.0.1" {
		if _, ok := network.Find(list, bindAddress); !ok {
			return s.mustState(), fmt.Errorf("本机当前没有 %s 这个 IPv4 地址，请重新选择", bindAddress)
		}
	}
	if err := validateDualSelection(list, bindAddress, additional); err != nil {
		return s.mustState(), err
	}

	prev := s.manager.Config()
	newCfg := hubmgr.Config{
		ConfigPath:              desktop.hubConfigPath,
		BindAddress:             bindAddress,
		AdditionalBindAddresses: append([]string(nil), additional...),
		Port:                    port,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.manager.Restart(ctx, newCfg); err != nil {
		friendly := hubmgr.FriendlyError(err)
		// Try to restore the previous selection so the user keeps a working Hub.
		restoreErr := s.startPrevious(prev)
		if restoreErr != nil {
			return s.mustState(), fmt.Errorf("%s\n（尝试恢复原配置也失败：%v）", friendly, restoreErr)
		}
		return s.mustState(), errors.New(friendly)
	}

	saved := appsettings.Settings{
		BindAddress:             bindAddress,
		Port:                    port,
		AdditionalBindAddresses: append([]string(nil), additional...),
	}
	if err := appsettings.Save(desktop.settingsPath, saved); err != nil {
		return s.mustState(), fmt.Errorf("Hub 已按新地址启动，但保存桌面设置失败：%w", err)
	}

	desktop.lastStartupErr = ""
	if desktop.app != nil {
		inbox := s.manager.URL()
		if fresh, err := network.List(); err == nil {
			if picked := preferredAccessURL(fresh, s.manager.URLs()); picked != "" {
				inbox = picked
			}
		}
		desktop.openInbox(inbox)
		desktop.showInbox()
	}
	return s.mustState(), nil
}

func (s *SettingsService) startPrevious(prev hubmgr.Config) error {
	if prev.BindAddress == "" || prev.Port == 0 {
		return nil
	}
	return s.manager.Start(prev)
}

func (s *SettingsService) mustState() State {
	st, err := s.GetState()
	if err != nil {
		return State{}
	}
	return st
}

// PairingCode is one freshly generated local pairing code. Code stays a string
// so a leading zero (for example "0073") survives the bridge and the UI.
type PairingCode struct {
	Code      string `json:"code"`
	ExpiresAt int64  `json:"expires_at"`
}

// PairedDevice is one device paired with this Hub. The raw device token is
// never part of this view (the Hub only stores its hash).
type PairedDevice struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"created_at"`
	LastSeenAt int64  `json:"last_seen_at"`
}

// errHubNotRunning is the settings bridge's answer when pairing is used without
// a live Hub: pairing only exists for a Hub that is actually listening, so the
// window says so instead of showing an empty list as if it were the truth.
func errHubNotRunning() error {
	return errors.New("Hub 未运行：请先在下方「网络绑定」中保存地址并启动 Hub，然后再添加设备")
}

// pairingError maps the Hub's lifecycle errors to that message and keeps every
// other failure (bad device id, revocation failure) as it is.
func pairingError(err error) error {
	if errors.Is(err, hub.ErrNotStarted) || errors.Is(err, hub.ErrClosed) {
		return errHubNotRunning()
	}
	return err
}

// CreatePairingCode asks the running Hub for a new local 4-digit code (120s
// lifetime, one use, leading zeros kept). It is only returned to this window:
// there is no HTTP endpoint that generates a code, and it is never logged.
func (s *SettingsService) CreatePairingCode() (PairingCode, error) {
	if s.manager == nil {
		return PairingCode{}, errHubNotRunning()
	}
	code, err := s.manager.CreatePairingCode()
	if err != nil {
		return PairingCode{}, pairingError(err)
	}
	return PairingCode{Code: code.Code, ExpiresAt: code.ExpiresAt}, nil
}

// ListPairedDevices returns the devices paired with this Hub so the settings
// window can show their names and times and offer revocation.
func (s *SettingsService) ListPairedDevices() ([]PairedDevice, error) {
	if s.manager == nil {
		return nil, errHubNotRunning()
	}
	devices, err := s.manager.ListPairedDevices()
	if err != nil {
		return nil, pairingError(err)
	}
	out := make([]PairedDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, PairedDevice{
			ID:         d.ID,
			Name:       d.Name,
			CreatedAt:  d.CreatedAt,
			LastSeenAt: d.LastSeenAt,
		})
	}
	return out, nil
}

// RevokePairedDevice revokes one paired device through the running Hub, which
// also invalidates its HTTP session and open WebSocket.
func (s *SettingsService) RevokePairedDevice(id string) error {
	if s.manager == nil {
		return errHubNotRunning()
	}
	if err := s.manager.RevokePairedDevice(id); err != nil {
		return pairingError(err)
	}
	return nil
}

// OpenInbox tells the shell to show the inbox window.
func (s *SettingsService) OpenInbox() {
	desktop.showInbox()
}

// OpenInBrowser opens the running Hub URL in the default browser.
func (s *SettingsService) OpenInBrowser() (string, error) {
	hubURL := preferredHubURL(s.manager)
	if hubURL == "" {
		return "", errors.New("Hub 未运行")
	}
	if s.app == nil {
		return hubURL, errors.New("桌面应用尚未就绪")
	}
	if err := s.app.Browser.OpenURL(hubURL + "/"); err != nil {
		return hubURL, err
	}
	return hubURL, nil
}

// Quit stops the Hub and exits the application.
func (s *SettingsService) Quit() {
	desktop.quit()
}
