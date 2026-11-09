package main

import (
	"log"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Raw messages the Hub-origin inbox window may send the shell. The list is a
// closed set of fixed actions: the page can never name a native method, a file
// or an arbitrary URL to execute. The origin/window guard lives in
// inboxControlAction and is enforced before any action runs.
const (
	rawOpenSettingsMessage = "flowdrop:opensettings"
	rawOpenURLPrefix       = "flowdrop:openurl:"
)

// inboxAction is the action decoded from one inbox raw message.
type inboxAction int

const (
	inboxActionNone inboxAction = iota
	inboxActionOpenSettings
	inboxActionOpenExternal
)

// inboxControlAction is the pure policy for inbox raw messages. It accepts a
// message only when:
//
//   - it comes from the inbox window (not the local settings window),
//   - the sender still sits on the Hub origin the inbox window was loaded
//     from, and
//   - the payload is one of the fixed actions below.
//
// Everything else is ignored. When the inbox window is reloaded onto another
// Hub address, hubOrigin is updated first, so a stale page cannot keep using
// the old origin.
func (d *desktopState) inboxControlAction(isInbox bool, msg string, origin *application.OriginInfo) (inboxAction, string) {
	if !isInbox || d.hubOrigin == "" || origin == nil {
		return inboxActionNone, ""
	}
	if originOf(origin.Origin) != d.hubOrigin {
		return inboxActionNone, ""
	}
	switch msg {
	case rawOpenSettingsMessage:
		return inboxActionOpenSettings, ""
	}
	if strings.HasPrefix(msg, rawOpenURLPrefix) {
		target := strings.TrimPrefix(msg, rawOpenURLPrefix)
		if target == "" || len(target) > 4096 || !isAllowedExternalURL(target) {
			return inboxActionNone, ""
		}
		return inboxActionOpenExternal, target
	}
	return inboxActionNone, ""
}

// dispatchInboxMessage runs an already-validated inbox action. The settings
// window is local and in-process; opening it never exposes the Wails management
// bridge to the Hub page itself.
func (d *desktopState) dispatchInboxMessage(isInbox bool, msg string, origin *application.OriginInfo) {
	action, target := d.inboxControlAction(isInbox, msg, origin)
	switch action {
	case inboxActionOpenSettings:
		d.showSettings()
	case inboxActionOpenExternal:
		// Cheap abuse guard: at most one external open per 500ms.
		now := time.Now().UnixMilli()
		if prev := d.lastExternalAt.Load(); now-prev < 500 {
			return
		}
		d.lastExternalAt.Store(now)
		if d.app != nil {
			if err := d.app.Browser.OpenURL(target); err != nil {
				log.Printf("open external url: %v", err)
			}
		}
	}
}

// desktopSettingsEntryJS is injected only into the Hub-origin inbox window by
// this process. It adds a clearly labelled 设置 button that is visible before
// login (login screen) and after login (next to 退出), and re-adds it after
// React replaces the DOM on login/logout. The button sends exactly one fixed
// raw message; it does not expose or call any arbitrary native binding.
//
// A normal browser never receives this script, so the Hub pages served to
// phones and browsers stay unchanged.
const desktopSettingsEntryJS = `(function () {
  if (window.__flowdropDesktopSettingsEntry) { return; }
  window.__flowdropDesktopSettingsEntry = true;
  var ID = "flowdrop-desktop-settings";
  var pending = 0;
  var BTN_STYLE = "font:inherit;font-size:0.8rem;line-height:1.2;padding:0.35rem 0.7rem;" +
    "border:1px solid var(--border,#c9ced6);border-radius:8px;background:var(--surface,#fff);" +
    "color:var(--text,#15181d);cursor:pointer;white-space:nowrap;";
  var FIXED_STYLE = "position:fixed;top:calc(36px + 0.6rem + env(safe-area-inset-top));right:0.7rem;" +
    "z-index:2147483647;box-shadow:0 1px 3px rgba(0,0,0,0.25);";

  function openSettings(ev) {
    if (ev) { ev.preventDefault(); ev.stopPropagation(); }
    try {
      window._wails.invoke("flowdrop:opensettings");
    } catch (e) {
      // Runtime injection and the first paint can race; retry once briefly.
      setTimeout(function () {
        try { window._wails.invoke("flowdrop:opensettings"); } catch (e2) {}
      }, 200);
    }
  }

  function makeButton() {
    var b = document.createElement("button");
    b.id = ID;
    b.type = "button";
    b.textContent = "设置";
    b.title = "打开 FlowDrop 本机设置（Ctrl+,）";
    b.setAttribute("aria-label", "打开 FlowDrop 本机设置");
    b.addEventListener("click", openSettings, true);
    return b;
  }

  function ensure() {
    if (!document.body) { return; }
    var existing = document.getElementById(ID);
    var right = document.querySelector(".topbar-right");
    if (right) {
      // Logged-in inbox: sit in the header next to 退出, so the entry never
      // floats over the composer or the item actions, even in a small window.
      if (existing && existing.parentElement === right) { return; }
      if (existing) { existing.remove(); }
      var inline = makeButton();
      inline.style.cssText = BTN_STYLE;
      right.insertBefore(inline, right.firstChild);
      return;
    }
    // Login / boot: no header exists yet. A small fixed button in the
    // top-right corner stays visible without covering the login form.
    if (existing) { return; }
    var fixed = makeButton();
    fixed.style.cssText = BTN_STYLE + FIXED_STYLE;
    document.body.appendChild(fixed);
  }

  function schedule() {
    if (pending) { return; }
    pending = window.requestAnimationFrame(function () {
      pending = 0;
      ensure();
    });
  }

  // React replaces the whole tree on login/logout. Re-add the button after any
  // replacement instead of depending on one render pass.
  new MutationObserver(schedule).observe(document.documentElement, {
    childList: true,
    subtree: true
  });
  ensure();
  schedule();
})();`
