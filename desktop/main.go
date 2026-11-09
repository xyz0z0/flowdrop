// Command flowdrop-desktop is the Linux desktop shell for FlowDrop.
//
// It runs the exact same Go Hub in-process (flowdrop/pkg/hub) and shows the
// existing React inbox in a native WebView window, plus a local-only settings
// window for the bind address, port and device pairing (code generation, device
// list, revocation).
//
// The inbox window needs no pairing code on this machine: while the local Hub
// starts, the shell pairs itself as one more native device (POST
// /api/v1/auth/pair with native=true, see desktop_login.go) and injects that
// long-lived device token into the page, which replays it as a Bearer token to
// get a normal session cookie. Pairing codes are only for phones, and the local
// device is shown as a non-revocable "本机桌面" row instead of a code prompt.
//
// Two windows, two origins, on purpose:
//
//   - "inbox" loads the Hub's own http://<bind>:<port>/ URL, so cookies, the
//     CSRF double-submit token, the Origin check, the WebSocket and
//     authenticated downloads all behave exactly as in a browser. That origin
//     cannot reach the Wails asset server, so no native binding is exposed to
//     the LAN-facing page.
//   - "settings" is served from the Wails asset server (wails://localhost),
//     which is in-process only and never listens on a network port. Only that
//     window can call the Go settings service. It is a transient dialog of the
//     inbox: created hidden, raised over the inbox by the native 设置 entry,
//     the tray or Ctrl+,, and it never leads the startup flow on its own (only
//     a failed Hub start shows it first, because that is where the fix is).
//
// The shell has two run modes, persisted in desktop.json and switchable at
// runtime from the settings window (no restart): local (the default, everything
// above) and remote, where the shell stops its own Hub and the inbox window
// loads an existing Hub on another machine. In remote mode pairing is done by
// that Hub's own page.
package main

import (
	"context"
	"embed"
	"errors"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/desktop/internal/hubmgr"
	"flowdrop/desktop/internal/network"
	"flowdrop/pkg/hub"
)

//go:embed all:assets
var assets embed.FS

// settingsWindowURL is the asset-server document of the native settings window.
// It only exists inside this process; nothing listens on a TCP port for it.
const settingsWindowURL = "/index.html"

// desktopState is the process-wide shell state shared by windows, tray and the
// bound settings service.
type desktopState struct {
	app     *application.App
	manager *hubmgr.Manager
	// settingsPath is the desktop's own JSON file (bind address + port only).
	settingsPath string
	// loginClient performs the shell's own pairing request against the local
	// Hub (POST /api/v1/auth/pair). It is deliberately separate from anything
	// the WebView does and has a hard timeout, so a Hub that accepts the port
	// but never answers cannot hang startup.
	loginClient *http.Client
	// deviceToken is the native device token the inbox page replays
	// (desktopBootstrapJS). It is written once at startup, before any window
	// exists, and read while the inbox window is created, hence atomic.
	deviceToken atomic.Value // string
	// hubConfigPath is the Hub's config file (listen/data_dir/...), kept
	// separate so the desktop never writes its own settings into it.
	hubConfigPath string
	inbox         *application.WebviewWindow
	settings      *application.WebviewWindow
	tray          *application.SystemTray
	trayAvailable bool
	quitting      atomic.Bool
	// startupErr is the last Hub startup failure shown in the settings window
	// ("" when there is none). It is written at startup, by ApplyMode and by
	// ApplyNetwork, and read by GetState from the settings RPC, so every access
	// goes through startupError/setStartupError.
	startupErr atomic.Value // string
	// remoteURL is the remote Hub base URL when this process runs in remote
	// mode (no local Hub is started), otherwise "". ApplyMode switches it at
	// runtime, and it is read from several goroutines (settings RPC, tray and
	// native-menu callbacks, window creation), so every access goes through
	// remoteHubURL/setRemoteHubURL.
	remoteURL atomic.Value // string
	// modeSwitch serialises run-mode switching. Two switches must never
	// interleave (stop/start Hub plus window reload), but it is never held while
	// reading remoteURL, so tray/JS callbacks cannot block behind a switch.
	modeSwitch     sync.Mutex
	hubOrigin      string // scheme://host[:port] the inbox window is allowed to be on
	lastExternalAt atomic.Int64
	// webviewProxyConfigured records that configureDesktopWebViewProxy()
	// applied the in-process WebKit NO_PROXY mode. Only that success may
	// suppress the old "add no_proxy" warning; a failure keeps the diagnosis.
	webviewProxyConfigured   atomic.Bool
	webviewProxyConfigureErr atomic.Value // string
	// a11yFallback records why guardAccessibilityBridge had to disable the
	// AT-SPI bridge for this launch (empty when accessibility is on). It is
	// shown in the settings window so the user is never silently muted.
	a11yFallback atomic.Value // string
}

var desktop desktopState

// logWriter receives every stdlib log and slog record. It starts as stderr and
// is widened to stderr+file when openLogFile succeeds (Windows GUI builds have
// no console, so stderr alone would discard the whole trail).
var logWriter io.Writer = os.Stderr

func main() {
	// Windows GUI builds have no console: mirror the log into a file so a user
	// can still send us a diagnosable trail. A failure here never blocks startup.
	if f, err := openLogFile(); err != nil {
		log.Printf("log file unavailable, logging to stderr only: %v", err)
	} else if f != nil {
		logWriter = io.MultiWriter(os.Stderr, f)
		log.SetOutput(logWriter)
	}
	// The accessibility bridge must be guarded before anything can initialise
	// GTK/WebKit: a broken session AT-SPI bus crashes libatk-bridge inside the
	// GTK main loop, which Go cannot recover from. Healthy sessions keep
	// accessibility; only a bus that fails a real round trip is bypassed for
	// this process, with the reason surfaced in the settings window.
	guardAccessibilityBridge()

	st, settingsPath, startupWarn := loadStartupSettings()
	// The Hub's records must land in the same sink as the shell's: on a
	// Windows GUI build stderr is gone, so without this the Hub would start
	// silently and fail silently.
	hubLogger := slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: hubLogLevel()}))
	manager := hubmgr.New()
	manager.Logger = hubLogger

	app := application.New(application.Options{
		Name:        "FlowDrop",
		Description: "FlowDrop 流转 — 个人跨设备收件箱",
		Icon:        iconPNG,
		Logger:      slog.New(slog.NewTextHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelWarn})),
		LogLevel:    slog.LevelWarn,
		Linux: application.LinuxOptions{
			// Hiding the inbox window must not quit the app: the Hub keeps
			// running in the tray until the user explicitly quits.
			DisableQuitOnLastWindowClosed: true,
			ApplicationID:                 "io.flowdrop.Desktop",
			ProgramName:                   "flowdrop-desktop",
		},
		Windows: application.WindowsOptions{
			// Same rule as the Linux options: hiding the inbox window must
			// not quit the app, otherwise "close to tray" would kill the Hub
			// the moment the last window goes away.
			DisableQuitOnLastWindowClosed: true,
			WndClass:                      "FlowDropDesktop",
			// The two windows only ever load this app's own Hub and in-process
			// wails:// assets, so the proxy must never be consulted: a system proxy
			// (Clash, corporate VPN) would otherwise swallow http://<bind>:<port>/.
			// Same intent as the Linux WebKitGTK direct-connection setup.
			AdditionalBrowserArgs: []string{"--no-proxy-server"},
		},
		SingleInstance: &application.SingleInstanceOptions{
			// Double-clicking the launcher twice must not start a second Hub
			// against the same data directory.
			UniqueID: "io.flowdrop.Desktop",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if desktop.app != nil {
					desktop.showInbox()
				}
			},
		},
		Services: []application.Service{
			application.NewService(newSettingsService(manager)),
		},
		KeyBindings: map[string]func(application.Window){
			// Keep a keyboard route to the local settings window: the main
			// window is the Hub origin and cannot call the Wails bridge, so
			// this action runs natively and only opens the in-process window.
			"Ctrl+,": func(application.Window) { desktop.showSettings() },
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(mustSub(assets)),
		},
		ErrorHandler: func(err error) { onAppError(err) },
		RawMessageHandler: func(w application.Window, msg string, origin *application.OriginInfo) {
			desktop.handleRawMessage(w, msg, origin)
		},
		OnShutdown: func() {
			// The tray "退出" path stops the Hub explicitly; this covers
			// window-manager/session-driven exits too. Stop is idempotent.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := manager.Stop(ctx); err != nil {
				log.Printf("stop hub during shutdown: %v", err)
			}
		},
	})

	desktop.app = app
	desktop.manager = manager
	desktop.settingsPath = settingsPath
	desktop.hubConfigPath = hubConfigPath()
	// Remote mode: the inbox window loads that Hub directly and this process
	// starts no Hub at all (no mDNS, no port, no data directory).
	desktop.setRemoteHubURL(st.RemoteURL)
	desktop.trayAvailable = trayWatcherPresent()
	// One shared client for the shell's own pairing request (see
	// ensureDesktopLogin): bounded, cookie-less and never reused for page
	// traffic, which goes through the WebView's own network stack.
	desktop.loginClient = &http.Client{Timeout: desktopLoginTimeout}
	syncServiceApp(app)

	// Downloads: Wails v3.1.0's GTK3 backend installs no download handler, so
	// WebKitGTK's default decide-destination saves into the XDG download
	// directory under the suggested filename with allow-overwrite FALSE. A
	// same-name file already there made the first click on "下载" damage that
	// file. Install the handler that always picks a free name before any
	// window exists (downloads_linux_gtk3.go); it deletes and overwrites
	// nothing.
	installDownloadHandler()

	// Configure the in-process WebKit network stack before any window/page is
	// created: the desktop windows only load this app's own Hub and the local
	// wails:// assets, so they can go direct while the host's system proxy
	// settings stay untouched. A failure keeps the accurate proxy diagnosis in
	// the settings window; it never prevents the Hub from starting.
	if err := configureDesktopWebViewProxy(); err != nil {
		desktop.webviewProxyConfigureErr.Store(err.Error())
		log.Printf("in-app WebView proxy bypass not configured; system proxy unchanged: %v", err)
	} else {
		desktop.webviewProxyConfigured.Store(true)
		log.Printf("in-app WebView network proxy: direct (system proxy unchanged)")
	}

	// The Hub must be started before the inbox window: the window loads the
	// real HTTP URL, which only exists once the port is bound. In remote mode
	// the Hub belongs to another machine, so nothing is started or advertised
	// here and the local proxy advice (about reaching this machine's LAN
	// address) does not apply.
	var startErr error
	if st.IsRemote() {
		log.Printf("remote hub mode: using %s (local hub not started)", st.RemoteURL)
	} else {
		startErr = startHub(manager, st, desktop.hubConfigPath)
		if warn := desktopProxyWarning(st.BindAddress); warn != "" {
			log.Printf("proxy warning: %s", warn)
		}
	}
	if startupWarn != nil {
		if st.IsRemote() {
			// A settings-file problem is never a reason to show the settings
			// window in remote mode: this process owns no Hub to fix.
			log.Printf("remote hub mode: startup warning: %v", startupWarn)
		} else {
			startErr = errors.Join(startupWarn, startErr)
		}
	}

	// Local mode: pair this window with the Hub that just started, so the inbox
	// opens logged in and no code is ever typed on this machine. A failure is
	// one log line and never blocks startup; the inbox still offers pairing.
	st = desktop.ensureDesktopLogin(st)

	desktop.createWindows(startErr)
	desktop.createTray()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

// hubLogLevel is the Hub's log level for the desktop shell: FLOWDROP_LOG_LEVEL
// when set (same parsing as the Hub itself), otherwise Info, which is the Hub's
// own default. Keeping them in sync means the desktop Hub is never quieter than
// a standalone Hub would be.
func hubLogLevel() slog.Level {
	level := slog.LevelInfo
	if v := os.Getenv("FLOWDROP_LOG_LEVEL"); v != "" {
		_ = level.UnmarshalText([]byte(v))
	}
	return level
}

// loadStartupSettings reads the desktop settings file. On first run it detects
// the machine's real IPv4 addresses, prefers a home-LAN address, and persists
// that choice so the next start is stable. It never guesses silently: the
// detected default is written to disk and shown in the settings window.
func loadStartupSettings() (st appsettings.Settings, settingsPath string, warn error) {
	settingsPath = appsettings.DefaultPath()
	saved, found, err := appsettings.Load(settingsPath)
	if err != nil {
		// A corrupt/unusable file must not stop the app: fall back to a fresh
		// detection and surface the problem in the settings window.
		warn = err
	}
	if found && err == nil {
		return saved, settingsPath, nil
	}
	def := appsettings.Default()
	list, lerr := network.List()
	if lerr != nil {
		return def, settingsPath, errors.Join(warn, lerr)
	}
	addr, reachable := network.DefaultBind(list)
	def.BindAddress = addr
	if !reachable {
		warn = errors.Join(warn, errors.New("本机没有可用的局域网或 Tailscale IPv4 地址，Hub 只能绑定到 127.0.0.1；手机暂时无法访问"))
	}
	if serr := appsettings.Save(settingsPath, def); serr != nil {
		warn = errors.Join(warn, serr)
	}
	return def, settingsPath, warn
}

// hubConfigPath is the Hub's own JSON config file location, mirroring the CLI's
// resolution so the desktop and a CLI Hub read the same operator settings.
func hubConfigPath() string { return hub.DefaultConfigPath() }

func startHub(manager *hubmgr.Manager, st appsettings.Settings, cfgPath string) error {
	err := manager.Start(hubmgr.Config{
		ConfigPath:              cfgPath,
		BindAddress:             st.BindAddress,
		AdditionalBindAddresses: append([]string(nil), st.AdditionalBindAddresses...),
		Port:                    st.Port,
	})
	if err != nil {
		log.Printf("hub start failed: %v", err)
	}
	return err
}

func mustSub(f embed.FS) fs.FS {
	sub, err := fs.Sub(f, "assets")
	if err != nil {
		panic(err)
	}
	return sub
}

// createWindows creates the inbox window (when the Hub is up) and always the
// settings window. The inbox leads in both modes: it is the only window the
// user normally needs, and the local shell is already logged in through its own
// device token. Only a Hub that failed to start (or has no URL at all) shows the
// settings window first, because that is where the address/port can be fixed.
func (d *desktopState) createWindows(startErr error) {
	d.settings = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:                       "settings",
		Title:                      "FlowDrop 设置",
		URL:                        settingsWindowURL,
		Width:                      780,
		Height:                     760,
		MinWidth:                   620,
		MinHeight:                  520,
		BackgroundColour:           application.NewRGB(24, 25, 28),
		DevToolsEnabled:            false,
		DefaultContextMenuDisabled: true,
		// The page draws its own title bar (window_chrome.go) instead of the
		// window manager's, so both windows look the same on every desktop.
		Frameless: true,
		JS:        settingsWindowChromeJS,
	})
	d.wireCloseToTray(d.settings)
	// Windows ignores the JS option for URL-loaded windows (injected_js_windows.go);
	// elsewhere this is a no-op because Wails injects the same script per load.
	installInjectedJS(d.settings, settingsWindowChromeJS)
	// KDE Plasma's Wayland session: tell KWin this frameless window draws its
	// own frame, otherwise KWin adds its title bar (with minimise/maximise/
	// close) on top of the page's own one (window_frame_linux_gtk3.go).
	ownWindowFrame(d.settings)
	// Frameless window, page-drawn title bar: the bar itself has to start the
	// window move (window_drag_linux_gtk3.go).
	installTitlebarDrag(d.settings)

	hubURL := d.inboxBaseURL()
	if startErr != nil || hubURL == "" {
		d.setStartupError(hubmgr.FriendlyError(startErr))
		d.showSettings()
		return
	}
	// The inbox always leads, locally and remotely: there is no "no device
	// paired yet" step any more, because the local shell pairs itself
	// (desktop_login.go) and a remote Hub owns its own pairing page.
	d.openInbox(hubURL)
	d.wireSettingsDialog()
	d.inbox.Show()
	d.inbox.Focus()
	// The settings window stays a transient dialog of the inbox: created and
	// wired, but hidden until the user asks for it.
	d.settings.Hide()
}

// inboxBaseURL is the URL the inbox window loads: the remote Hub in remote
// mode, otherwise this process's own Hub.
func (d *desktopState) inboxBaseURL() string {
	if remote := d.remoteHubURL(); remote != "" {
		return remote
	}
	return preferredHubURL(d.manager)
}

// remoteHubURL returns the remote Hub base URL this process runs against, or ""
// in local mode. It is safe to call from any goroutine and never blocks behind a
// mode switch in progress.
func (d *desktopState) remoteHubURL() string {
	v, _ := d.remoteURL.Load().(string)
	return v
}

// setRemoteHubURL switches the runtime mode: a non-empty URL means "remote Hub"
// (this process owns no Hub), "" means local mode.
func (d *desktopState) setRemoteHubURL(raw string) {
	d.remoteURL.Store(raw)
}

// startupError returns the last Hub startup failure shown in the settings
// window, or "".
func (d *desktopState) startupError() string {
	v, _ := d.startupErr.Load().(string)
	return v
}

// setStartupError records (or with "" clears) that startup failure.
func (d *desktopState) setStartupError(msg string) {
	d.startupErr.Store(msg)
}

// reloadInbox points the inbox window at hubURL, creating and showing it when
// the startup path never did (for example the local Hub failed to start, so only
// the settings window exists). Without a Wails app (unit tests) it is a no-op.
func (d *desktopState) reloadInbox(hubURL string) {
	if d.app == nil || hubURL == "" {
		return
	}
	existed := d.inbox != nil
	d.openInbox(hubURL)
	if !existed {
		d.showInbox()
	}
}

// wireSettingsDialog makes the settings window a transient dialog of the inbox
// (window_owner_linux_gtk3.go): centred over it, kept above it and hidden with
// it. It is idempotent and a no-op before both windows exist, so it is called
// both after the startup order above and after every window (re)creation.
func (d *desktopState) wireSettingsDialog() {
	if d.inbox == nil || d.settings == nil {
		return
	}
	attachSettingsDialog(d.inbox, d.settings)
}

// preferredHubURL picks the URL the desktop opens by default: the bound LAN
// address when one exists, otherwise the Hub's primary URL. In dual mode the
// Hub's primary URL is already the LAN listener, so this also covers the case
// where the interface list changed between start and window creation.
func preferredHubURL(m *hubmgr.Manager) string {
	if m == nil {
		return ""
	}
	urls := m.URLs()
	if list, err := network.List(); err == nil {
		if picked := preferredAccessURL(list, urls); picked != "" {
			return picked
		}
	}
	return m.URL()
}

// openInbox creates the inbox window that loads the Hub's real HTTP origin.
func (d *desktopState) openInbox(hubURL string) {
	if d.inbox == nil {
		d.inbox = d.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:     "inbox",
			Title:    "FlowDrop 流转",
			URL:      hubURL + "/",
			Width:    980,
			Height:   780,
			MinWidth: 420, MinHeight: 420,
			BackgroundColour:           application.NewRGB(24, 25, 28),
			DevToolsEnabled:            false,
			DefaultContextMenuDisabled: true,
			// The Hub page is our own React app; the injected scripts only relay
			// *external* link clicks to the system browser (see
			// desktopLinkInterceptionJS), add the desktop-only 设置 entry, and
			// provide the page half of the native FileList bridge plus the
			// "don't navigate to the dropped file" guard
			// (desktopFileDropGuardJS). Downloads are answered by the native
			// WebKitGTK destination handler (downloads_linux_gtk3.go), which
			// always picks a free name and never touches an existing file.
			//
			// No window menu: a GTK menu bar would add a second, always-visible
			// row of 设置 / 在浏览器中打开 / 退出 buttons on top of the page's own
			// header (the page already carries the desktop 设置 entry, and the
			// tray owns the browser/quit actions). Ctrl+, still reaches the
			// settings window through KeyBindings.
			//
			// The device token is injected here and nowhere else: it never
			// reaches the HTML asset, localStorage or a log line, and the script
			// (desktopBootstrapJS) only replays it against /api/v1/auth/session.
			Frameless: true,
			JS:        inboxInjectedJS(d.desktopDeviceToken()),
		})
		d.wireCloseToTray(d.inbox)
		// Same Windows gap as the settings window: re-run the inbox script set
		// (login bootstrap, title bar, 设置 entry, link/drop bridges) per load.
		installInjectedJS(d.inbox, inboxInjectedJS(d.desktopDeviceToken()))
		ownWindowFrame(d.inbox)
		installTitlebarDrag(d.inbox)
		// Linux GTK: restore WebKit's HTML5 drop for this window only and install
		// the native FileList bridge there (filedrop_linux_gtk3.go). The dropped
		// local paths stay in native state; the page only ever receives real
		// File objects through its own hidden file input. Wails'
		// EnableFileDrop stays false (default), so no native path API is exposed.
		d.installInboxFileDropRelax()
	} else {
		d.inbox.SetURL(hubURL + "/")
	}
	d.hubOrigin = originOf(hubURL)
	if d.tray != nil {
		d.tray.SetTooltip("FlowDrop — 收件箱运行中")
	}
	// The inbox may have just been (re)created, so the transient-parent link and
	// the centring constraint have to be (re)applied: the old marker points at
	// the old window.
	d.wireSettingsDialog()
}

// desktopInboxBootstrapJS returns the extra script that logs the inbox in with
// the shell's own device token, preceded by the newline the other snippets are
// joined with. With no token (remote mode, failed pairing) it returns nothing at
// all, so the injected JS is byte-for-byte what it was before.
func desktopInboxBootstrapJS(token string) string {
	if token == "" {
		return ""
	}
	return "\n" + desktopBootstrapJS(token)
}

// showInbox raises the inbox window, creating it if the Hub came up later.
func (d *desktopState) showInbox() {
	if d.inbox == nil {
		if hubURL := d.inboxBaseURL(); hubURL != "" {
			d.openInbox(hubURL)
		} else {
			d.showSettings()
			return
		}
	}
	if d.inbox != nil {
		d.inbox.Show()
		d.inbox.Focus()
	}
}

func (d *desktopState) showSettings() {
	if d.settings == nil {
		return
	}
	d.settings.Show()
	d.settings.Focus()
	// Show/Focus alone cannot raise a window above its parent on every desktop:
	// present it natively as well (it also re-attaches the transient parent when
	// the inbox was re-created).
	presentSettingsDialog(d.inbox, d.settings)
}

// wireCloseToTray turns the window close button into "hide to tray", but only
// when a tray is actually available. Without a tray the window would become
// unreachable, so the close button asks to quit instead.
func (d *desktopState) wireCloseToTray(w *application.WebviewWindow) {
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if d.quitting.Load() {
			return
		}
		if !d.trayAvailable || d.tray == nil {
			e.Cancel()
			d.confirmQuit(w)
			return
		}
		e.Cancel()
		w.Hide()
	})
}

// confirmQuit is the no-tray fallback: never leave a background process the
// user cannot reach.
func (d *desktopState) confirmQuit(w *application.WebviewWindow) {
	q := d.app.Dialog.Question().
		SetTitle("退出 FlowDrop？").
		SetMessage("当前没有可用的系统托盘，关闭窗口将退出 FlowDrop 并停止 Hub。").
		AttachToWindow(w)
	q.AddButton("取消").SetAsCancel()
	yes := q.AddButton("退出 FlowDrop").SetAsDefault()
	yes.OnClick(func() { d.quit() })
	q.Show()
}

// quit stops the Hub (withdrawing mDNS and releasing the port) and then exits.
func (d *desktopState) quit() {
	if d.quitting.Swap(true) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.manager.Stop(ctx); err != nil {
		log.Printf("stop hub: %v", err)
	}
	if d.app != nil {
		d.app.Quit()
	}
}

// createTray advertises the running Hub and provides the only explicit quit
// path that also stops the Hub.
func (d *desktopState) createTray() {
	if !d.trayAvailable {
		log.Printf("no StatusNotifierWatcher on the session bus: running without a tray; closing the window will ask to quit")
		return
	}
	tray := d.app.SystemTray.New()
	tray.SetIcon(trayPNG)
	tray.SetTooltip("FlowDrop — 收件箱")
	menu := d.app.NewMenu()
	menu.Add("显示收件箱").OnClick(func(*application.Context) { d.showInbox() })
	menu.Add("设置…").OnClick(func(*application.Context) { d.showSettings() })
	menu.Add("在浏览器中打开").OnClick(func(*application.Context) {
		if hubURL := d.inboxBaseURL(); hubURL != "" {
			if err := d.app.Browser.OpenURL(hubURL + "/"); err != nil {
				log.Printf("open browser: %v", err)
			}
		}
	})
	menu.AddSeparator()
	menu.Add("退出 FlowDrop（停止 Hub）").OnClick(func(*application.Context) { d.quit() })
	tray.SetMenu(menu)
	tray.OnClick(func() { d.showInbox() })
	d.tray = tray
}

// onAppError watches Wails/tray failures. If the tray turns out to be
// unusable, the windows are restored and closing asks to quit — the user must
// never end up with a hidden window and no tray icon.
func onAppError(err error) {
	if err == nil {
		return
	}
	log.Printf("wails error: %v", err)
	if strings.Contains(err.Error(), "systray error") && desktop.trayAvailable {
		desktop.trayAvailable = false
		desktop.tray = nil
		log.Printf("tray unavailable at runtime: restoring windows and switching to confirm-quit")
		if desktop.app != nil {
			if desktop.inbox != nil {
				desktop.inbox.Show()
			}
			if desktop.settings != nil {
				desktop.settings.Show()
			}
		}
	}
}

// isAllowedExternalURL restricts the shell-opened URLs to plain web links.
// Custom schemes (file:, javascript:, wails:, data:) are never handed to the
// system browser from the Hub-origin page.
func isAllowedExternalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != ""
}

func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// handleRawMessage accepts only the closed set of fixed actions implemented by
// settings_entry.go: open the local settings window, or open one validated
// http(s) URL in the system browser. It runs only for this process's inbox
// window while that window is on the current Hub origin; the local settings
// window cannot be driven by the Hub page and no arbitrary native method is
// reachable.
func (d *desktopState) handleRawMessage(w application.Window, msg string, origin *application.OriginInfo) {
	if d.inbox == nil || w == nil {
		return
	}
	// Window-chrome buttons (minimise/maximise/close) come from the same two
	// windows; they are checked first because the settings window may send them
	// too and never reaches the inbox-only actions below.
	if d.runWindowAction(w, msg, origin) {
		return
	}
	d.dispatchInboxMessage(w.ID() == d.inbox.ID(), msg, origin)
}

// desktopLinkInterceptionJS is injected into the Hub-origin inbox window. It
// only rewrites clicks on links that point at another origin (or window.open)
// so they open in the system browser instead of navigating the app window away
// from the Hub. Same-origin links (including authenticated downloads) are left
// to WebKit.
const desktopLinkInterceptionJS = `(function () {
  if (window.__flowdropDesktopLinks) { return; }
  window.__flowdropDesktopLinks = true;
  function send(raw) {
    try {
      var u = new URL(raw, window.location.href);
      if (u.protocol !== "http:" && u.protocol !== "https:") { return false; }
      if (u.origin === window.location.origin) { return false; }
      window._wails.invoke("flowdrop:openurl:" + u.href);
      return true;
    } catch (e) { return false; }
  }
  document.addEventListener("click", function (ev) {
    if (ev.defaultPrevented || ev.button !== 0) { return; }
    var el = ev.target;
    while (el && el !== document.body) {
      if (el.tagName === "A" && el.hasAttribute("href")) { break; }
      el = el.parentElement;
    }
    if (!el || el.tagName !== "A") { return; }
    if (el.hasAttribute("download")) { return; }
    if (send(el.getAttribute("href"))) {
      ev.preventDefault();
      ev.stopPropagation();
    }
  }, true);
  var originalOpen = window.open;
  window.open = function (u) {
    if (u && send(String(u))) { return null; }
    return originalOpen.apply(window, arguments);
  };
})();`

// joinInjectedScripts concatenates injected snippets so that a snippet that
// throws at top level cannot stop the ones after it, and so that the login
// bootstrap (which the page cannot work without) is not the last thing to run.
//
// Why this is needed: Wails injects WebviewWindowOptions.JS on a window load
// event, and that event can land on the very first document before the Hub page
// exists. The title bar survives that because it waits for document.body, but a
// snippet that touches the page immediately either throws (killing every later
// statement of the same script) or is lost with the document it ran in. Each
// snippet already guards itself with a window flag, so isolating them costs
// nothing and keeps a failure to one feature.
func joinInjectedScripts(snippets ...string) string {
	parts := make([]string, 0, len(snippets))
	for _, snippet := range snippets {
		if snippet == "" {
			continue
		}
		parts = append(parts, "try {\n"+snippet+"\n} catch (e) { /* flowdrop: isolated snippet */ }")
	}
	return strings.Join(parts, "\n")
}

// inboxInjectedJS is the whole script set the inbox window receives. The login
// bootstrap comes first: without it the page sits on the pairing screen, so it
// must not depend on any other snippet running.
func inboxInjectedJS(token string) string {
	return joinInjectedScripts(
		desktopInboxBootstrapJS(token),
		desktopWindowChromeJS,
		desktopLinkInterceptionJS,
		desktopSettingsEntryJS,
		desktopFileDropGuardJS,
	)
}
