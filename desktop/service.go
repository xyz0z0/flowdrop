package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"slices"
	"strings"
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
	// Mode is the saved run mode: "local" or "remote". The mode is required in
	// desktop.json, so this always reports one of the two.
	Mode string `json:"mode"`
	// RemoteURL is the saved remote Hub base URL in remote mode, otherwise "".
	RemoteURL     string            `json:"remote_url"`
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
	// RemoteWarning is a pure hint for the settings window: it is only set in
	// remote mode, and only when the saved remote address sits in the same IPv4
	// /24 as one of this machine's addresses (two different physical networks
	// that both use 192.168.x.x look exactly like this). It never affects saving
	// or the running mode.
	RemoteWarning string `json:"remote_warning"`
	// AutoBind is the saved "bind every local address" switch. It is a saved
	// preference, not a claim about the listeners: the live address set stays
	// in URLs.
	AutoBind bool `json:"auto_bind"`
	// AccessibilityWarning explains that this launch had to disable the
	// AT-SPI bridge because the session's accessibility bus was broken. It is
	// empty when accessibility is enabled.
	AccessibilityWarning string `json:"accessibility_warning"`
	// LocalDeviceName is the name this window itself is paired under
	// ("本机桌面 · host") in local mode once a device token is saved, otherwise
	// "". It is shown as a non-revocable row above the phone/tablet devices:
	// the window logs in with that token, so there is no code to type and no way
	// to revoke it without locking this window out (revoking it here would only
	// make the shell pair itself again on the next start).
	LocalDeviceName string `json:"local_device_name"`
}

// GetState returns the current shell state for the settings window.
func (s *SettingsService) GetState() (State, error) {
	// The saved file is authoritative for the mode the next start will use; the
	// running process only tells whether that mode is already active.
	saved, savedOK := loadSavedSettings()
	mode, remoteURL := appsettings.ModeLocal, ""
	if savedOK {
		mode = saved.Mode
		if mode == appsettings.ModeRemote {
			remoteURL = saved.RemoteURL
		}
	}
	st := State{
		Mode:          mode,
		RemoteURL:     remoteURL,
		ConfigPath:    desktop.settingsPath,
		StartupError:  desktop.startupError(),
		TrayAvailable: desktop.trayAvailable,
		URLs:          []string{},
		AccessURLs:    []AccessURL{},
		Additional:    []string{},
	}
	if !savedOK {
		// No usable file: report what this process actually does.
		if remote := desktop.remoteHubURL(); remote != "" {
			st.Mode, st.RemoteURL = appsettings.ModeRemote, remote
		}
	}
	remote := st.Mode == appsettings.ModeRemote
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

	if !remote && s.manager != nil {
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
	// In remote mode this process owns no Hub, so nothing here is running: the
	// window must not show local URLs that do not exist.
	if remote {
		st.Running = false
		st.URL = ""
		st.URLs = nil
		st.AccessURLs = nil
		// Checked against the already fetched interface list; non-remote mode
		// leaves this empty.
		st.RemoteWarning = remoteSubnetConflict(st.RemoteURL, list)
	}
	// When the Hub failed to start (or does not exist in remote mode) there is
	// no running config; show what the user asked for so the failing or hidden
	// address/port stays visible and editable.
	if !st.Running && savedOK {
		st.BindAddress = saved.BindAddress
		st.Port = saved.Port
		st.Additional = append([]string{}, saved.AdditionalBindAddresses...)
	}
	st.DualMode = len(st.Additional) > 0
	// Auto binding is a saved preference; without a usable file, report what
	// this process actually runs so the checkbox never silently resets.
	st.AutoBind = savedOK && saved.AutoBind
	if !savedOK && !remote && s.manager != nil {
		st.AutoBind = s.manager.Config().AutoBind
	}
	// Only actual bound URLs are shown as usable. Configured addresses that are
	// not running remain visible as the requested inputs, never as access URLs.
	if st.Running {
		st.AccessURLs = describeAccessURLs(list, st.URLs)
	}
	// The proxy advice is about reaching this machine's Hub from a phone; in
	// remote mode there is no local listener to reach.
	if st.BindAddress != "" && !remote {
		host := st.BindAddress
		if inbox := preferredAccessURL(list, st.URLs); inbox != "" {
			if u, err := url.Parse(inbox); err == nil {
				host = u.Hostname()
			}
		}
		st.ProxyWarning = desktopProxyWarning(host)
	}
	st.AccessibilityWarning = desktopAccessibilityWarning(desktop.a11yFallbackReason())
	// Only a local Hub has this window as a device: in remote mode the token is
	// either absent or belongs to another Hub's database.
	if !remote && savedOK && saved.DesktopDeviceToken != "" {
		st.LocalDeviceName = desktopDeviceName()
	}
	if listErr != nil && st.LastError == "" {
		st.LastError = listErr.Error()
	}
	return st, nil
}

// loadSavedSettings reads desktop.json for the window. A missing or unusable
// file is not an error here: the window still has to render, and GetState falls
// back to what this process actually does.
func loadSavedSettings() (appsettings.Settings, bool) {
	if desktop.settingsPath == "" {
		return appsettings.Settings{}, false
	}
	saved, found, err := appsettings.Load(desktop.settingsPath)
	if err != nil || !found {
		if err != nil {
			log.Printf("read desktop settings for state: %v", err)
		}
		return appsettings.Settings{}, false
	}
	return saved, true
}

// remoteHubMode reports that no local Hub exists to drive: either this process
// already runs in remote mode, or the saved file asks for remote mode and has
// been edited by hand while the process still runs the local Hub. Both cases
// must answer the pairing/network methods with a readable error instead of
// pretending to work.
func (s *SettingsService) remoteHubMode() bool {
	if s.runningRemote() {
		return true
	}
	saved, found := loadSavedSettings()
	return found && saved.IsRemote()
}

// runningRemote reports whether this process currently runs against a remote Hub
// (and owns no local one).
func (s *SettingsService) runningRemote() bool { return desktop.remoteHubURL() != "" }

// errRemoteMode is the settings bridge's answer in remote mode: this machine
// runs no Hub, so pairing and network changes belong to the remote one.
func errRemoteMode() error {
	return errors.New("当前为远程 Hub 模式，本机没有 Hub；请先在「运行模式」里切回「本机 Hub」再操作")
}

// ApplyMode saves the run mode (local Hub or an existing remote Hub) and applies
// it to the running process right away: switching local -> remote stops the
// in-process Hub, switching remote -> local starts it, and both reload the inbox
// window and every other URL consumer (tray, native menu, browser) without a
// restart. When a step fails the saved settings stay as they were and the
// process keeps the mode it was working in.
func (s *SettingsService) ApplyMode(mode, remoteURL string) (State, error) {
	// A switch stops/starts the Hub and reloads the window: two switches must
	// never interleave. remoteURL readers do not take this lock, so tray/JS
	// callbacks are never blocked behind a switch in progress.
	desktop.modeSwitch.Lock()
	defer desktop.modeSwitch.Unlock()

	saved, found := loadSavedSettings()
	if !found {
		// Seed the listen selection from the running Hub (or the built-in
		// default) so saving a mode never loses the address/port the user uses.
		saved = appsettings.Default()
		if s.manager != nil {
			if cfg := s.manager.Config(); cfg.BindAddress != "" && cfg.Port != 0 {
				saved.BindAddress = cfg.BindAddress
				saved.Port = cfg.Port
				saved.AdditionalBindAddresses = append([]string(nil), cfg.AdditionalBindAddresses...)
			}
		}
	}
	next := saved
	next.Mode = strings.TrimSpace(mode)
	next.RemoteURL = remoteURL
	next = next.Normalize()
	// One validation path for the mode and the remote URL: appsettings owns the
	// rules, the bridge never keeps a copy of them.
	if err := next.Validate(); err != nil {
		return s.mustState(), err
	}
	// Saving a remote Hub address requires that address to answer: otherwise the
	// window only shows an empty inbox and the user cannot tell "wrong address"
	// from "no Hub there". local -> remote, remote -> remote and a repeated save
	// of the same remote address all go through this one check. It runs under the
	// mode switch lock (at most remoteProbeTimeout), which keeps the order of the
	// lock and the probe fixed.
	if next.IsRemote() {
		if err := probeRemoteHub(context.Background(), next.RemoteURL); err != nil {
			return s.mustState(), errors.New(remoteProbeFailureMessage(next.RemoteURL, err))
		}
	}

	// Idempotent: this process already runs exactly this mode and address, so
	// only the file has to agree (saving it again in that case is not needed and
	// no Hub is stopped or started).
	if s.runningRemote() == next.IsRemote() && desktop.remoteHubURL() == next.RemoteURL {
		if !found || !sameSettings(saved, next) {
			if err := appsettings.Save(desktop.settingsPath, next); err != nil {
				return s.mustState(), fmt.Errorf("保存运行模式失败：%w", err)
			}
		}
		return s.mustState(), nil
	}

	if next.IsRemote() {
		return s.switchToRemote(next)
	}
	return s.switchToLocal(next)
}

// switchToRemote stops this process's Hub and points the shell at the remote one.
// The runtime mode, the window and the saved file only change once the Hub is
// really down, so a failed stop leaves the app working in local mode.
func (s *SettingsService) switchToRemote(next appsettings.Settings) (State, error) {
	if s.manager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := s.manager.Stop(ctx)
		cancel()
		if err != nil {
			return s.mustState(), fmt.Errorf("停止本机 Hub 失败：%s", hubmgr.FriendlyError(err))
		}
	}
	// From here on the Hub is gone: finish the switch so the shell is never left
	// half-way, then report a save failure separately.
	desktop.setRemoteHubURL(next.RemoteURL)
	desktop.setStartupError("")
	// reloadInbox also refreshes hubOrigin, so the file-drop bridge and the
	// native message guard follow the new origin instead of the old one.
	desktop.reloadInbox(next.RemoteURL)
	if err := appsettings.Save(desktop.settingsPath, next); err != nil {
		return s.mustState(), fmt.Errorf("本机 Hub 已停止并切到远程 Hub，但保存桌面设置失败：%w", err)
	}
	return s.mustState(), nil
}

// switchToLocal starts this process's Hub from the saved listen selection
// (BindAddress/Port/Additional). A failed start keeps the runtime remote mode,
// the remote inbox window and the saved settings exactly as they were.
func (s *SettingsService) switchToLocal(next appsettings.Settings) (State, error) {
	if err := startHub(s.manager, next, desktop.hubConfigPath); err != nil {
		return s.mustState(), fmt.Errorf("启动本机 Hub 失败：%s", hubmgr.FriendlyError(err))
	}
	desktop.setRemoteHubURL("")
	desktop.setStartupError("")
	desktop.reloadInbox(preferredHubURL(s.manager))
	if err := appsettings.Save(desktop.settingsPath, next); err != nil {
		return s.mustState(), fmt.Errorf("本机 Hub 已启动，但保存桌面设置失败：%w", err)
	}
	return s.mustState(), nil
}

// sameSettings compares two normalized settings field by field (Settings holds a
// slice, so it is not comparable with ==).
func sameSettings(a, b appsettings.Settings) bool {
	return a.Mode == b.Mode && a.RemoteURL == b.RemoteURL &&
		a.BindAddress == b.BindAddress && a.Port == b.Port &&
		slices.Equal(a.AdditionalBindAddresses, b.AdditionalBindAddresses)
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
	return s.applyNetwork(bindAddress, nil, false, port)
}

// ApplyNetworkConfig applies the complete listen selection: a primary address
// plus optional additional addresses (dual mode stores exactly one Tailscale
// address next to the LAN primary) on one shared port. The desktop bridge is
// the only caller; there is no network management endpoint.
func (s *SettingsService) ApplyNetworkConfig(bindAddress string, additional []string, port int) (State, error) {
	return s.applyNetwork(bindAddress, additional, false, port)
}

// ApplyDualNetwork is the settings window's explicit dual-mode entry point:
// one LAN address and one Tailscale address, both bound by the same Hub on the
// same port. An empty Tailscale address is rejected instead of quietly falling
// back to single mode.
func (s *SettingsService) ApplyDualNetwork(lanAddress, tailscaleAddress string, port int) (State, error) {
	if tailscaleAddress == "" {
		return s.mustState(), errors.New("双模式需要选择 Tailscale 地址；如果只想用一个地址，请切换到单地址模式")
	}
	return s.applyNetwork(lanAddress, []string{tailscaleAddress}, false, port)
}

// ApplyAutoNetwork is the settings window's "bind every local address" entry
// point: one concrete primary address/port (auto binding shares that port) and
// the switch that makes the Hub cover every eligible LAN/Tailscale address.
// autoBind=false is exactly single-address mode, so unticking the checkbox
// needs no different call.
func (s *SettingsService) ApplyAutoNetwork(bindAddress string, autoBind bool, port int) (State, error) {
	return s.applyNetwork(bindAddress, nil, autoBind, port)
}

// applyNetwork validates, restarts and persists a listen selection. It stops the
// old Hub and starts a new one against the same data directory, then reloads the
// inbox window onto the new URL. All listeners are bound by that one Hub/one
// database/one data-directory lock; a partially bound set is never reported as
// success. If the new selection cannot be bound, the previous configuration is
// started again so the app is not left without a Hub.
func (s *SettingsService) applyNetwork(bindAddress string, additional []string, autoBind bool, port int) (State, error) {
	if s.remoteHubMode() {
		return s.mustState(), errRemoteMode()
	}
	if err := appsettings.ValidateBindAddress(bindAddress); err != nil {
		return s.mustState(), err
	}
	if err := appsettings.ValidatePort(port); err != nil {
		return s.mustState(), err
	}
	// Auto binding covers every eligible address itself, so an explicit extra
	// address is refused by the same rule the saved file enforces.
	if err := appsettings.ValidateAutoBindSelection(autoBind, additional); err != nil {
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
		AutoBind:                autoBind,
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

	// Start from the saved file so everything this call does not own survives:
	// the device identity the shell paired with (dropping it would re-pair on
	// the next launch and leave a dead device row behind) and any future field.
	saved, found := loadSavedSettings()
	if !found {
		saved = appsettings.Default()
	}
	// This path only runs while the shell is in local mode (remoteHubMode above
	// rejects the rest), so the file keeps local mode.
	saved.Mode = appsettings.ModeLocal
	saved.BindAddress = bindAddress
	saved.Port = port
	saved.AdditionalBindAddresses = append([]string(nil), additional...)
	saved.AutoBind = autoBind
	if err := appsettings.Save(desktop.settingsPath, saved); err != nil {
		return s.mustState(), fmt.Errorf("Hub 已按新地址启动，但保存桌面设置失败：%w", err)
	}

	desktop.setStartupError("")
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
	// Payload is the frozen flowdrop://pair... string encoded in the QR code. It
	// carries the same secret as Code and is only ever returned to this window.
	// It is empty when the Hub has no phone-reachable address, and the empty
	// field is omitted so the zero value of this DTO keeps its old JSON shape.
	Payload string `json:"payload,omitempty"`
	// QRDataURL is a data:image/png;base64 URL for Payload, or empty when no
	// usable address exists or PNG generation failed (the text code still works).
	QRDataURL string `json:"qr_data_url,omitempty"`
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
	if s.remoteHubMode() {
		return PairingCode{}, errRemoteMode()
	}
	if s.manager == nil {
		return PairingCode{}, errHubNotRunning()
	}
	code, err := s.manager.CreatePairingCode()
	if err != nil {
		return PairingCode{}, pairingError(err)
	}
	// The QR shortcut only advertises addresses the phone can actually reach:
	// the Hub's real listener URLs, never the configured list and never
	// 127.0.0.1/::1. Without one there is no QR, and the text code is enough.
	payload, qrDataURL := pairingCodeView(code.Code, s.manager.URLs())
	return PairingCode{
		Code:      code.Code,
		ExpiresAt: code.ExpiresAt,
		Payload:   payload,
		QRDataURL: qrDataURL,
	}, nil
}

// PairingCodeActive reports whether the running Hub still has an unused,
// unexpired pairing code. The settings window polls it so a code a phone has
// just consumed is removed within one refresh instead of waiting for the 120s
// countdown; only a boolean crosses the bridge.
func (s *SettingsService) PairingCodeActive() (bool, error) {
	if s.remoteHubMode() {
		return false, errRemoteMode()
	}
	if s.manager == nil {
		return false, errHubNotRunning()
	}
	active, err := s.manager.PairingCodeActive()
	if err != nil {
		return false, pairingError(err)
	}
	return active, nil
}

// ListPairedDevices returns the devices paired with this Hub so the settings
// window can show their names and times and offer revocation. The window's own
// device (the one this process logs in with, see LocalDeviceName) is left out on
// purpose: it is shown as a separate, non-revocable row.
func (s *SettingsService) ListPairedDevices() ([]PairedDevice, error) {
	if s.remoteHubMode() {
		return nil, errRemoteMode()
	}
	if s.manager == nil {
		return nil, errHubNotRunning()
	}
	devices, err := s.manager.ListPairedDevices()
	if err != nil {
		return nil, pairingError(err)
	}
	localID := ""
	if saved, found := loadSavedSettings(); found {
		localID = saved.DesktopDeviceID
	}
	return visibleDevices(devices, localID), nil
}

// visibleDevices maps the Hub's device records to the settings view and drops
// the local window's own record. It is a plain function so the filtering rule
// can be tested without a running Hub.
func visibleDevices(devices []hub.PairedDevice, localDeviceID string) []PairedDevice {
	out := make([]PairedDevice, 0, len(devices))
	for _, d := range devices {
		if localDeviceID != "" && d.ID == localDeviceID {
			continue
		}
		out = append(out, PairedDevice{
			ID:         d.ID,
			Name:       d.Name,
			CreatedAt:  d.CreatedAt,
			LastSeenAt: d.LastSeenAt,
		})
	}
	return out
}

// RevokePairedDevice revokes one paired device through the running Hub, which
// also invalidates its HTTP session and open WebSocket.
func (s *SettingsService) RevokePairedDevice(id string) error {
	if s.remoteHubMode() {
		return errRemoteMode()
	}
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

// OpenInBrowser opens the inbox URL in the default browser: the remote Hub in
// remote mode, otherwise the running local Hub.
func (s *SettingsService) OpenInBrowser() (string, error) {
	hubURL := desktop.inboxBaseURL()
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
