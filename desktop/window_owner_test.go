package main

import (
	"os"
	"strings"
	"testing"
)

// These are static assertions: they check that the settings-dialog helper keeps
// the shape the GTK task requires, without opening a window. The behaviour
// itself (a real transient dialog on a live compositor) is verified in the GUI
// task, not here.

func readAsset(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func TestSettingsDialogGTKHelperUsesMainThreadAndParent(t *testing.T) {
	src := readAsset(t, "window_owner_linux_gtk3.go")
	for _, want := range []string{
		"//go:build linux && cgo && gtk3",
		"withNativeWindows(", // retries on the GTK thread until the native window exists
		"gtk_window_set_transient_for(",
		"GTK_WIN_POS_CENTER_ON_PARENT",
		"gtk_window_present_with_time(",
		"gtk_window_present(",
		"gtk_get_current_event_time(",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("window_owner_linux_gtk3.go no longer contains %q", want)
		}
	}
	// The windows exist before app.Run, so a synchronous hop onto the main thread
	// before the loop runs deadlocks the start (window_frame_linux_gtk3.go).
	if strings.Contains(src, "application.InvokeSync(") {
		t.Error("window_owner_linux_gtk3.go must not use application.InvokeSync")
	}
	// The parent link has to be in place before the position constraint.
	if strings.Index(src, "gtk_window_set_transient_for(") > strings.Index(src, "GTK_WIN_POS_CENTER_ON_PARENT") {
		t.Error("transient parent must be set before the CENTER_ON_PARENT constraint")
	}
}

func TestSettingsFooterHasNoInboxButton(t *testing.T) {
	// The inbox entry lives in the tray menu and the inbox title bar; the footer
	// duplicate was removed. remote-open-inbox belongs to the remote-Hub card and
	// stays.
	html := readAsset(t, "assets/index.html")
	foot := html[strings.Index(html, "<footer class=\"foot\">"):]
	if strings.Contains(foot, `id="open-inbox"`) {
		t.Error("settings footer still offers a duplicate inbox entry")
	}
	if !strings.Contains(foot, `id="quit"`) {
		t.Error("settings footer lost its quit button")
	}
	js := readAsset(t, "assets/app.js")
	if strings.Contains(js, `$("open-inbox")`) {
		t.Error("app.js still binds the removed footer inbox button")
	}
}
