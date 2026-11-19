package main

import (
	"strings"
	"testing"
)

// Static assertions only: the Windows injection path cannot be executed on this
// machine (no Windows runtime), so the tests pin the shape Main relies on. The
// real behaviour — title bar, buttons and passwordless login on a Windows
// machine — is unverified here.

func TestInjectedJSWindowsHooksEveryNavigation(t *testing.T) {
	src := readAsset(t, "injected_js_windows.go")
	for _, want := range []string{
		"//go:build windows",
		"events.Windows.WebViewNavigationCompleted",
		"func installInjectedJS(w *application.WebviewWindow, js string)",
		"if w == nil || js == \"\" {",
		"go w.ExecJS(js)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("injected_js_windows.go must contain %q, source:\n%s", want, src)
		}
	}
}

func TestInjectedJSOtherIsNoOp(t *testing.T) {
	src := readAsset(t, "injected_js_other.go")
	if !strings.Contains(src, "//go:build !windows") {
		t.Fatalf("injected_js_other.go must be built for everything but Windows, source:\n%s", src)
	}
	if !strings.Contains(src, "func installInjectedJS(*application.WebviewWindow, string) {}") {
		t.Fatalf("injected_js_other.go must keep an empty installInjectedJS body, source:\n%s", src)
	}
	if strings.Contains(src, "ExecJS(") {
		t.Fatalf("Linux/macOS already get the scripts from WebviewWindowOptions.JS; injecting twice is wrong, source:\n%s", src)
	}
}

func TestMainInstallsInjectedJSForBothWindows(t *testing.T) {
	src := readAsset(t, "main.go")
	for _, want := range []string{
		"installInjectedJS(d.settings, settingsWindowChromeJS)",
		"installInjectedJS(d.inbox, inboxInjectedJS(d.desktopDeviceToken()))",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("main.go must call %q, so a URL-loaded window still gets its page scripts on Windows", want)
		}
	}
	if n := strings.Count(src, "installInjectedJS("); n != 2 {
		t.Fatalf("main.go must call installInjectedJS exactly twice (settings, inbox), got %d", n)
	}
	// The JS option has to stay: Linux and macOS are injected through it.
	for _, want := range []string{"JS:        settingsWindowChromeJS", "JS:        inboxInjectedJS(d.desktopDeviceToken())"} {
		if !strings.Contains(src, want) {
			t.Fatalf("main.go must keep the Wails JS option %q for Linux/macOS", want)
		}
	}
}
