package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// desktopFileDropGuardJS is injected only into the Hub-origin inbox window.
//
// It is the page half of the native FileList bridge (see
// filedrop_linux_gtk3.go): Linux WebKitGTK does not hand a native file drop to
// the page, so the GTK side receives the drop, keeps the real OS paths in C,
// and calls back into this script through exactly one entry point,
// window.__flowdropDesktopFileDropImport(acceptToken, directories, hasFiles).
//
// Contract of this script:
//
//   - A hidden <input type=file multiple> is the only way native paths become
//     a real File/FileList. Native code sets no file path here; it only sets
//     the input's accept token and clicks the input, and WebKit's own
//     run-file-chooser callback answers with the pending native files.
//   - The drop is refused unless <html data-flowdrop-drop-ready="true">. That
//     attribute is set by the Composer (which only mounts after login), so a
//     native drop on the login page or an unmounted inbox never opens a
//     chooser. The attribute is also not a native path channel: the page can
//     only say "ready", never which file to read.
//   - The input change handler converts input.files to a plain File[] and
//     dispatches CustomEvent('flowdrop:desktop-files', { detail: { files,
//     directories } }). directory names are hints only (basenames, already
//     percent-decoded here); no path is involved. It never reads file content
//     (no FileReader/base64/Blob caching): the existing Composer upload path
//     consumes the File objects directly.
//   - input.value and the per-drop directory state are cleared before the
//     event, so dropping the same file twice works and no state leaks into the
//     next drop.
//   - The navigation guard stays: for file drags (dataTransfer.types has
//     "Files") the default drop action is cancelled so WebKit never navigates
//     the window to file:///..., while plain text/URL drags are untouched.
//     The event still propagates to the page's own handlers.
//
// window.__flowdropDesktopDrag(active) is the optional native hover signal: it
// only dispatches CustomEvent('flowdrop:desktop-drag', { detail: { active } })
// so the Composer can toggle its existing overlay.
const desktopFileDropGuardJS = `(function () {
  if (window.__flowdropDesktopFileDropBridge) { return; }
  window.__flowdropDesktopFileDropBridge = true;

  var input = null;
  var pendingDirectories = [];

  function dropReady() {
    var root = document.documentElement;
    return !!root && root.getAttribute("data-flowdrop-drop-ready") === "true";
  }

  function resetInput() {
    if (input) {
      input.value = "";
      input.removeAttribute("accept");
    }
    pendingDirectories = [];
  }

  function onInputChange() {
    if (!input) { return; }
    var files = Array.from(input.files || []);
    var directories = pendingDirectories;
    // Clear before dispatching: the same file must produce a new change event
    // on the next native drop, and a throwing listener must not keep state.
    resetInput();
    window.dispatchEvent(new CustomEvent("flowdrop:desktop-files", {
      detail: { files: files, directories: directories }
    }));
  }

  function ensureInput() {
    if (input && input.parentNode) { return input; }
    input = document.createElement("input");
    input.type = "file";
    input.multiple = true;
    input.setAttribute("data-flowdrop-desktop-file-input", "true");
    input.style.position = "fixed";
    input.style.left = "-10000px";
    input.style.top = "0";
    input.style.width = "1px";
    input.style.height = "1px";
    input.style.opacity = "0";
    input.addEventListener("change", onInputChange, false);
    document.documentElement.appendChild(input);
    return input;
  }

  function decodeDirectories(raw) {
    var out = [];
    if (!raw || !raw.length) { return out; }
    for (var i = 0; i < raw.length; i++) {
      var item = String(raw[i]);
      try { out.push(decodeURIComponent(item)); } catch (e) { out.push(item); }
    }
    return out;
  }

  // Entry point called by native code only. Returns false (and does nothing at
  // all) when the page is not ready to receive a desktop drop.
  window.__flowdropDesktopFileDropImport = function (acceptToken, directories, hasFiles) {
    if (!dropReady()) { return false; }
    var dirs = decodeDirectories(directories);
    if (!hasFiles) {
      // Directory-only drop: no chooser, just the basename hint.
      window.dispatchEvent(new CustomEvent("flowdrop:desktop-files", {
        detail: { files: [], directories: dirs }
      }));
      return true;
    }
    var el = ensureInput();
    pendingDirectories = dirs;
    el.value = "";
    // The unique per-drop MIME token scopes the native run-file-chooser
    // callback to this drop; an ordinary page file input never carries it.
    el.accept = String(acceptToken || "");
    el.click();
    return true;
  };

  // Optional native drag hover: toggles the existing Composer overlay only.
  window.__flowdropDesktopDrag = function (active) {
    window.dispatchEvent(new CustomEvent("flowdrop:desktop-drag", {
      detail: { active: !!active }
    }));
    return true;
  };

  function isFileDrag(dataTransfer) {
    if (!dataTransfer || !dataTransfer.types) { return false; }
    var types = dataTransfer.types;
    for (var i = 0; i < types.length; i++) {
      if (types[i] === "Files") { return true; }
    }
    return false;
  }

  function guard(ev) {
    if (!isFileDrag(ev.dataTransfer)) { return; }
    // Only cancel WebKit's default "navigate to the dropped file". The event
    // keeps propagating to the page's own handlers.
    ev.preventDefault();
  }

  window.addEventListener("dragover", guard, false);
  window.addEventListener("drop", guard, false);
})();
`

// installInboxFileDropRelax schedules the window-scoped removal of Wails' GTK
// drag-and-drop blockers plus the native FileList bridge for this process's
// inbox window.
//
// Timing: the WebView widget only exists once the window has been created on
// the GTK main thread, so this waits for the window's first finished page load
// and then hops onto that thread before touching any GTK object. The hook must
// not wait for the Wails runtime: the inbox window loads the Hub's own HTTP
// origin, not Wails' asset server, so the runtime-ready handshake is not a
// signal this window is guaranteed to produce. events.Linux.WindowLoadFinished
// comes from GTK's native load-changed callback (WEBKIT_LOAD_FINISHED) for
// this WebView, which already exists at that point and does not depend on the
// page's JavaScript. Later reloads/navigations fire it again, which re-arms the
// bridge with the current Hub origin and keeps it idempotent per view.
// The inbox window is not on the Hub origin's asset server, so nothing here is
// reachable from a page.
func (d *desktopState) installInboxFileDropRelax() {
	if d.inbox == nil {
		return
	}
	win := d.inbox
	win.RegisterHook(events.Linux.WindowLoadFinished, func(*application.WindowEvent) {
		application.InvokeAsync(func() { relaxInboxFileDrop(win, d.hubOrigin) })
	})
}
