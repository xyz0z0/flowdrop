package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDesktopFileDropBridgeHasNoContentReads pins the parts of the bridge that
// are not about behaviour but about the security boundary: the injected script
// must not read file content, must not cache blobs and must not call any native
// binding. Real File objects go to the page's existing upload path untouched.
func TestDesktopFileDropBridgeHasNoContentReads(t *testing.T) {
	for _, want := range []string{
		`window.__flowdropDesktopFileDropImport = function (acceptToken, directories, hasFiles)`,
		`data-flowdrop-drop-ready`,
		`"flowdrop:desktop-files"`,
		`"flowdrop:desktop-drag"`,
		`input.multiple = true`,
		`Array.from(input.files || [])`,
	} {
		if !strings.Contains(desktopFileDropGuardJS, want) {
			t.Errorf("file drop bridge is missing %q", want)
		}
	}
	// stopPropagation would break the Hub's own drag handling; FileReader,
	// base64 or Blob caching would mean the whole file is buffered in JS; a
	// native/invoke channel would bypass the authenticated upload path.
	for _, forbidden := range []string{
		"stopPropagation",
		"stopImmediatePropagation",
		"_wails",
		"Call.ByName",
		"FileReader",
		"readAs",
		"webkitGetAsEntry",
		"btoa(",
		"fetch(",
	} {
		if strings.Contains(desktopFileDropGuardJS, forbidden) {
			t.Errorf("file drop bridge must not contain %q", forbidden)
		}
	}
}

// nodeDropBridgeDriver exercises the injected script in a Node VM against a
// minimal DOM stub: no browser, no display, no GTK. It fails by throwing, so a
// non-zero exit code is a test failure.
const nodeDropBridgeDriver = `
const vm = require("vm");
const assert = require("assert");
const GUARD = __GUARD__;

function makePage() {
  const windowListeners = {};
  const events = [];
  const page = { events: events, clicks: 0, created: 0, input: null };

  const html = {
    attrs: {},
    getAttribute: function (name) {
      return Object.prototype.hasOwnProperty.call(this.attrs, name) ? this.attrs[name] : null;
    },
    setAttribute: function (name, value) { this.attrs[name] = String(value); },
    appendChild: function (node) { node.parentNode = html; },
  };

  function makeInput() {
    page.created++;
    const listeners = {};
    const input = {
      type: "",
      multiple: false,
      value: "",
      accept: "",
      files: [],
      parentNode: null,
      style: {},
      attributes: {},
      listeners: listeners,
      setAttribute: function (n, v) { this.attributes[n] = String(v); },
      removeAttribute: function (n) { delete this.attributes[n]; if (n === "accept") { this.accept = ""; } },
      addEventListener: function (n, fn) { (listeners[n] = listeners[n] || []).push(fn); },
      click: function () { page.clicks++; },
    };
    page.input = input;
    return input;
  }

  const document = {
    documentElement: html,
    createElement: function () { return makeInput(); },
  };

  const window = {
    addEventListener: function (n, fn) { (windowListeners[n] = windowListeners[n] || []).push(fn); },
    dispatchEvent: function (ev) { events.push(ev); return true; },
  };

  class CustomEvent {
    constructor(type, init) { this.type = type; this.detail = init ? init.detail : undefined; }
  }

  page.documentElement = html;
  const sandbox = {
    window: window,
    document: document,
    CustomEvent: CustomEvent,
    Array: Array,
    String: String,
    decodeURIComponent: decodeURIComponent,
    console: console,
  };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(GUARD, sandbox);
  return { page: page, window: window, windowListeners: windowListeners, events: events };
}

// 1. Not ready: no input is created, nothing is clicked, no event fires.
const notReady = makePage();
assert.strictEqual(notReady.window.__flowdropDesktopFileDropImport("application/x-flowdrop-drop-1", ["dir"], true), false, "import must refuse while the page is not ready");
assert.strictEqual(notReady.page.created, 0, "no input may be created before the page is ready");
assert.strictEqual(notReady.page.clicks, 0, "no click may happen before the page is ready");
assert.strictEqual(notReady.events.length, 0, "no event may fire before the page is ready");

// 2. Ready, directory-only drop: an event with empty files and the decoded
//    hints, still no file chooser.
// The ready attribute is read lazily on every import, so setting it after the
// script was injected is enough (that is what the Composer does on mount).
function makeReadyPage() {
  const instance = makePage();
  instance.page.documentElement.setAttribute("data-flowdrop-drop-ready", "true");
  return instance;
}

const dirDrop = makeReadyPage();
assert.strictEqual(dirDrop.window.__flowdropDesktopFileDropImport("application/x-flowdrop-drop-dir", ["dir%20one", "%E4%B8%AD%E6%96%87"], false), true, "a ready page must accept a directory drop");
assert.strictEqual(dirDrop.page.clicks, 0, "a directory-only drop must not open a file chooser");
assert.strictEqual(dirDrop.events.length, 1, "a directory-only drop dispatches one event");
assert.strictEqual(dirDrop.events[0].type, "flowdrop:desktop-files");
assert.deepStrictEqual(Array.from(dirDrop.events[0].detail.files), [], "directory-only drop has no files");
assert.deepStrictEqual(Array.from(dirDrop.events[0].detail.directories), ["dir one", "中文"], "directory hints are percent-decoded basenames");

// 3. Ready, file drop: the input gets the per-drop accept token and is clicked once.
const fileDrop = makeReadyPage();
const token = "application/x-flowdrop-drop-0123456789abcdef";
assert.strictEqual(fileDrop.window.__flowdropDesktopFileDropImport(token, ["folder"], true), true, "a ready page must accept a file drop");
assert.strictEqual(fileDrop.page.clicks, 1, "a file drop clicks the hidden input exactly once");
assert.strictEqual(fileDrop.page.input.accept, token, "the hidden input carries the per-drop accept token");
assert.strictEqual(fileDrop.page.input.value, "", "the input value is clear before the chooser");
assert.strictEqual(fileDrop.page.events.length, 0, "nothing is dispatched until the input change arrives");

// 4. change: File objects are dispatched as-is, state is cleared.
const file = { name: "report 报告.txt", size: 39, type: "text/plain" };
fileDrop.page.input.files = [file];
fileDrop.page.input.value = "C:\\fakepath\\report 报告.txt";
fileDrop.page.input.listeners.change[0]();
assert.strictEqual(fileDrop.page.events.length, 1, "change dispatches one event");
const chunk = fileDrop.page.events[0];
assert.strictEqual(chunk.type, "flowdrop:desktop-files");
assert.strictEqual(chunk.detail.files.length, 1, "the File[] is passed through");
assert.strictEqual(chunk.detail.files[0], file, "the exact File object is passed through, not a copy or a path");
assert.deepStrictEqual(Array.from(chunk.detail.directories), ["folder"], "the drop's directory hints travel with the files");
assert.strictEqual(fileDrop.page.input.value, "", "the input value is cleared so the same file can be dropped again");
assert.strictEqual(fileDrop.page.input.accept, "", "the accept token is cleared after the drop");

// 5. Dropping the same file again must produce another event.
assert.strictEqual(fileDrop.window.__flowdropDesktopFileDropImport(token, [], true), true, "the same batch can be repeated");
assert.strictEqual(fileDrop.page.clicks, 2, "second drop clicks again");
fileDrop.page.input.files = [file];
fileDrop.page.input.listeners.change[0]();
assert.strictEqual(fileDrop.page.events.length, 2, "second change dispatches again");

// 6. Hover only toggles the overlay event.
fileDrop.window.__flowdropDesktopDrag(true);
fileDrop.window.__flowdropDesktopDrag(false);
assert.strictEqual(fileDrop.events[2].type, "flowdrop:desktop-drag");
assert.strictEqual(fileDrop.events[2].detail.active, true);
assert.strictEqual(fileDrop.events[3].detail.active, false);

// 7. The navigation guard still cancels only file drags and never swallows them.
let prevented = 0;
const fileDrag = { dataTransfer: { types: ["Files", "text/uri-list"] }, preventDefault: function () { prevented++; } };
const textDrag = { dataTransfer: { types: ["text/plain"] }, preventDefault: function () { prevented++; } };
for (const fn of fileDrop.windowListeners.dragover) { fn(fileDrag); }
for (const fn of fileDrop.windowListeners.drop) { fn(fileDrag); }
assert.strictEqual(prevented, 2, "a file drag is prevented on dragover and drop");
for (const fn of fileDrop.windowListeners.dragover) { fn(textDrag); }
for (const fn of fileDrop.windowListeners.drop) { fn(textDrag); }
assert.strictEqual(prevented, 2, "plain text drags are left alone");

// 8. No content reading primitives are part of the page bridge.
assert.strictEqual(typeof fileDrop.window.FileReader, "undefined", "the bridge must not touch FileReader");

console.log("drop bridge behaviour ok");
`

// TestDesktopFileDropBridgePageBehaviour runs the injected script (the exact
// string the WebView gets) in a Node VM with a DOM stub. This covers the ready
// gate, the empty-input directory event, repeated drops and the navigation
// guard without a browser, WebKit or GTK.
func TestDesktopFileDropBridgePageBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available; page-side bridge behaviour not checked in this environment")
	}
	guard, err := json.Marshal(desktopFileDropGuardJS)
	if err != nil {
		t.Fatalf("marshal bridge script: %v", err)
	}
	script := strings.Replace(nodeDropBridgeDriver, "__GUARD__", string(guard), 1)
	path := filepath.Join(t.TempDir(), "drop-bridge-driver.cjs")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatalf("write driver: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, path).CombinedOutput()
	if err != nil {
		t.Fatalf("node drop-bridge driver failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "drop bridge behaviour ok") {
		t.Fatalf("node drop-bridge driver produced unexpected output: %s", out)
	}
}
