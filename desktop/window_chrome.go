package main

import (
	"strconv"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Raw messages sent by the two window-chrome buttons sets (the injected bar in
// the Hub-origin inbox window, and the settings page's own bar). Like the other
// raw messages these carry no arguments: the page can ask for one of three
// fixed window operations, never for a native method by name.
const (
	rawWindowMinimise = "flowdrop:window:minimise"
	rawWindowMaximise = "flowdrop:window:maximise"
	rawWindowClose    = "flowdrop:window:close"
)

// runWindowAction applies one window-chrome message. It accepts the inbox
// window only while it still sits on the Hub origin it was loaded from, and the
// local settings window (wails://localhost, in-process only) unconditionally.
// Anything else is ignored, so a page that somehow ends up in the inbox window
// cannot drive the window without being the Hub.
func (d *desktopState) runWindowAction(w application.Window, msg string, origin *application.OriginInfo) bool {
	if w == nil {
		return false
	}
	isInbox := d.inbox != nil && w.ID() == d.inbox.ID()
	isSettings := d.settings != nil && w.ID() == d.settings.ID()
	if !isInbox && !isSettings {
		return false
	}
	if isInbox && (origin == nil || d.hubOrigin == "" || originOf(origin.Origin) != d.hubOrigin) {
		return false
	}
	win := d.inbox
	if isSettings {
		win = d.settings
	}
	switch msg {
	case rawWindowMinimise:
		win.Minimise()
	case rawWindowMaximise:
		win.ToggleMaximise()
	case rawWindowClose:
		win.Close()
	default:
		return false
	}
	return true
}

// desktopWindowChromeCSS is the title bar's whole style. It is injected into
// the Hub page as one <style> element, so it never depends on the Hub's own
// stylesheet and cannot leak into the phone/browser UI (the script is only
// executed inside this process's inbox window).
const desktopWindowChromeCSS = `
:root { --flowdrop-chrome-top: 36px; }
#flowdrop-desktop-titlebar {
  position: fixed; top: 0; left: 0; right: 0; height: var(--flowdrop-chrome-top);
  display: flex; align-items: center; gap: 8px;
  padding: 0 6px 0 12px;
  /* Same tokens the Hub page paints its own background with, so the bar and
     the page below it are one colour in both light and dark mode. */
  background: var(--bg, #f4f4f6); color: var(--fg, #1c1c21);
  border-bottom: 1px solid var(--line-2, #ececf0);
  font: 500 13px/1 -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei UI", "Microsoft YaHei", system-ui, sans-serif;
  letter-spacing: .1px;
  z-index: 2147483647;
  --wails-draggable: drag;
  user-select: none; -webkit-user-select: none;
}
body.flowdrop-has-titlebar { padding-top: var(--flowdrop-chrome-top) !important; }
#flowdrop-desktop-titlebar .ftb-mark {
  width: 18px; height: 18px; border-radius: 5px; background: var(--accent, #2563eb);
  display: grid; place-items: center; flex: 0 0 auto;
}
#flowdrop-desktop-titlebar .ftb-mark svg { width: 12px; height: 12px; display: block; }
#flowdrop-desktop-titlebar .ftb-spacer { flex: 1 1 auto; }
#flowdrop-desktop-titlebar .ftb-btns {
  display: flex; align-items: center; gap: 2px; --wails-draggable: none;
}
#flowdrop-desktop-titlebar .ftb-btns button {
  width: 34px; height: 28px; padding: 0; border: 0; border-radius: 7px;
  background: transparent; color: inherit; cursor: default;
  display: grid; place-items: center;
  --wails-draggable: none;
}
#flowdrop-desktop-titlebar .ftb-btns button:hover { background: rgba(20, 22, 30, .07); }
#flowdrop-desktop-titlebar .ftb-btns button.ftb-close:hover { background: #d64545; color: #ffffff; }
#flowdrop-desktop-titlebar svg { display: block; }
@media (prefers-color-scheme: dark) {
  #flowdrop-desktop-titlebar .ftb-btns button:hover { background: rgba(255, 255, 255, .09); }
  #flowdrop-desktop-titlebar .ftb-btns button.ftb-close:hover { background: #b23b3b; }
}
`

// desktopWindowChromeJS draws the app's own title bar in the frameless inbox
// window: brand mark, name, and minimise/maximise/close. The bar itself is the
// GTK drag region (--wails-draggable: drag); the buttons opt out so a click on
// them is a click, not a window move. Window operations travel as the three
// fixed raw messages above; the page gets no other native capability.
var desktopWindowChromeJS = `(function () {
  if (window.__flowdropDesktopChrome) { return; }
  window.__flowdropDesktopChrome = true;
  var ID = "flowdrop-desktop-titlebar";
  var CSS = ` + strconv.Quote(desktopWindowChromeCSS) + `;
  var MARK = '<svg viewBox="0 0 16 16" fill="none" stroke="#ffffff" stroke-width="1.9" ' +
    'stroke-linecap="round" stroke-linejoin="round"><path d="M3.2 6.2h9.1l-2.1-2.1"/>' +
    '<path d="M12.8 9.8H3.7l2.1 2.1"/></svg>';
  var GLYPH = {
    minimise: '<svg width="12" height="12" viewBox="0 0 12 12"><path d="M2.5 6h7" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>',
    maximise: '<svg width="12" height="12" viewBox="0 0 12 12"><rect x="2.6" y="2.6" width="6.8" height="6.8" rx="1.4" fill="none" stroke="currentColor" stroke-width="1.3"/></svg>',
    close: '<svg width="12" height="12" viewBox="0 0 12 12"><path d="M3 3l6 6M9 3l-6 6" stroke="currentColor" stroke-width="1.3" stroke-linecap="round"/></svg>'
  };
  function send(action) {
    try { window._wails.invoke("flowdrop:window:" + action); } catch (e) {}
  }
  function button(action, label, extra) {
    return '<button type="button" data-ftb="' + action + '" aria-label="' + label +
      '" title="' + label + '"' + (extra || '') + '>' + GLYPH[action] + '</button>';
  }
  function build() {
    if (!document.body) { return; }
    var style = document.getElementById(ID + "-style");
    if (!style) {
      style = document.createElement("style");
      style.id = ID + "-style";
      style.textContent = CSS;
      (document.head || document.documentElement).appendChild(style);
    }
    document.body.classList.add("flowdrop-has-titlebar");
    if (document.getElementById(ID)) { return; }
    var bar = document.createElement("div");
    bar.id = ID;
    bar.setAttribute("role", "presentation");
    bar.innerHTML = '<span class="ftb-mark">' + MARK + '</span>' +
      '<span class="ftb-spacer"></span>' +
      '<span class="ftb-btns">' + button("minimise", "最小化") +
        button("maximise", "最大化") + button("close", "关闭", ' class="ftb-close"') + '</span>';
    bar.addEventListener("click", function (ev) {
      var el = ev.target;
      while (el && el !== bar && !(el.getAttribute && el.getAttribute("data-ftb"))) {
        el = el.parentElement;
      }
      if (!el || el === bar || !el.getAttribute) { return; }
      ev.preventDefault();
      ev.stopPropagation();
      send(el.getAttribute("data-ftb"));
    }, true);
    document.body.insertBefore(bar, document.body.firstChild);
  }
  build();
  // React owns #root, not body, so the bar normally survives every render; this
  // only covers a page or an extension clearing body children wholesale.
  new MutationObserver(build).observe(document.documentElement, { childList: true, subtree: false });
})()`

// settingsWindowChromeJS wires the settings page's static title bar to the same
// three messages. It is separate from desktopWindowChromeJS because that page
// ships its own HTML/CSS instead of receiving an injected bar.
const settingsWindowChromeJS = `(function () {
  var actions = [["tb-min", "minimise"], ["tb-max", "maximise"], ["tb-close", "close"]];
  for (var i = 0; i < actions.length; i++) {
    var el = document.getElementById(actions[i][0]);
    if (!el) { continue; }
    (function (action) {
      el.addEventListener("click", function (ev) {
        ev.preventDefault();
        ev.stopPropagation();
        try { window._wails.invoke("flowdrop:window:" + action); } catch (e) {}
      });
    })(actions[i][1]);
  }
})()`
