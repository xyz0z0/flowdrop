// Command flowdrop-desktop is the Linux desktop shell for FlowDrop.
//
// It runs the exact same Go Hub in-process (flowdrop/pkg/hub) and shows the
// existing React inbox in a native WebView window, plus a local-only settings
// window for the bind address, port and the first admin password.
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
//     window can call the Go settings service.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"log/slog"
	"net/url"
	"os"
	"strings"
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
	// hubConfigPath is the Hub's config file (listen/data_dir/...), kept
	// separate so the desktop never writes its own settings into it.
	hubConfigPath  string
	inbox          *application.WebviewWindow
	settings       *application.WebviewWindow
	tray           *application.SystemTray
	trayAvailable  bool
	quitting       atomic.Bool
	lastStartupErr string
	hubOrigin      string // scheme://host[:port] the inbox window is allowed to be on
	lastExternalAt atomic.Int64
}

var desktop desktopState

func main() {
	st, settingsPath, startupWarn := loadStartupSettings()
	manager := hubmgr.New()

	app := application.New(application.Options{
		Name:        "FlowDrop",
		Description: "FlowDrop 流转 — 个人跨设备收件箱",
		Icon:        iconPNG,
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		LogLevel:    slog.LevelWarn,
		Linux: application.LinuxOptions{
			// Hiding the inbox window must not quit the app: the Hub keeps
			// running in the tray until the user explicitly quits.
			DisableQuitOnLastWindowClosed: true,
			ApplicationID:                 "io.flowdrop.Desktop",
			ProgramName:                   "flowdrop-desktop",
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
	desktop.trayAvailable = trayWatcherPresent()
	syncServiceApp(app)

	// The Hub must be started before the inbox window: the window loads the
	// real HTTP URL, which only exists once the port is bound.
	startErr := startHub(manager, st, desktop.hubConfigPath)
	if warn := network.ProxyWarning(st.BindAddress); warn != "" {
		log.Printf("proxy warning: %s", warn)
	}
	if startupWarn != nil {
		startErr = errors.Join(startupWarn, startErr)
	}

	desktop.createWindows(startErr)
	desktop.createTray()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
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
// settings window. When the Hub failed to start, the settings window is shown
// with the error so the user can fix the address/port — no fake success.
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
	})
	d.wireCloseToTray(d.settings)

	hubURL := preferredHubURL(d.manager)
	if startErr != nil || hubURL == "" {
		d.lastStartupErr = hubmgr.FriendlyError(startErr)
		d.showSettings()
		return
	}
	d.openInbox(hubURL)
	// First run: no admin password exists yet, so lead with the local settings
	// window (the Hub deliberately has no remote initialisation endpoint).
	if ok, err := d.manager.HasAdmin(); err == nil && !ok {
		d.showSettings()
		return
	}
	d.settings.Hide()
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
			// The Hub page is our own React app; the injected script only
			// forwards *external* link clicks to the system browser (see
			// desktopLinkInterceptionJS). Downloads keep native WebKit
			// behaviour, verified to save into ~/Downloads.
			JS: desktopLinkInterceptionJS,
		})
		d.wireCloseToTray(d.inbox)
	} else {
		d.inbox.SetURL(hubURL + "/")
	}
	d.hubOrigin = originOf(hubURL)
	if d.tray != nil {
		d.tray.SetTooltip("FlowDrop — 收件箱运行中")
	}
}

// showInbox raises the inbox window, creating it if the Hub came up later.
func (d *desktopState) showInbox() {
	if d.inbox == nil {
		if hubURL := preferredHubURL(d.manager); hubURL != "" {
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
		if hubURL := preferredHubURL(d.manager); hubURL != "" {
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

// handleRawMessage handles the one narrow message the Hub-origin page may send
// the shell: "open this external URL in the system browser". The desktop never
// exposes the settings bridge to that origin (it cannot reach the Wails asset
// server), so this is the only native action available to it.
func (d *desktopState) handleRawMessage(w application.Window, msg string, origin *application.OriginInfo) {
	const prefix = "flowdrop:openurl:"
	if !strings.HasPrefix(msg, prefix) {
		return
	}
	if d.inbox == nil || w == nil || w.ID() != d.inbox.ID() {
		return
	}
	// The page must still be on the Hub origin it was loaded from.
	if d.hubOrigin == "" || origin == nil || originOf(origin.Origin) != d.hubOrigin {
		return
	}
	target := strings.TrimPrefix(msg, prefix)
	if len(target) > 4096 {
		return
	}
	if !isAllowedExternalURL(target) {
		return
	}
	u, err := url.Parse(target)
	if err != nil {
		return
	}
	// Cheap abuse guard: at most one external open per 500ms.
	now := time.Now().UnixMilli()
	if prev := d.lastExternalAt.Load(); now-prev < 500 {
		return
	}
	d.lastExternalAt.Store(now)
	if err := d.app.Browser.OpenURL(u.String()); err != nil {
		log.Printf("open external url: %v", err)
	}
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
