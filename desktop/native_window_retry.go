package main

// Native-window retry plumbing for the GTK helpers.
//
// The desktop creates every window before app.Run, and Wails only builds the
// underlying native window on the GTK thread once the main loop is running. An
// install that needs that handle (the KWin client-side-decoration announce,
// the settings dialog's transient parent) therefore cannot assume it exists on
// its first turn: in the isolated container run of 2026-11-09 the first attempt
// saw a nil handle for both windows, so the CSD announce silently did nothing
// and KWin would have drawn its own title bar again.
//
// These helpers retry on the GTK thread for a bounded time instead of giving up
// on the first miss. A failure is still only a log line: the window keeps
// working, it just keeps the default decoration behaviour.

import (
	"log"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	// nativeWindowRetryInterval is short enough to finish before a user can
	// notice, and long enough not to spin the GTK loop.
	nativeWindowRetryInterval = 80 * time.Millisecond
	// nativeWindowRetryAttempts bounds the wait to about two seconds.
	nativeWindowRetryAttempts = 25
)

// withNativeWindows runs apply with the native handles of every window, once
// all of them exist. It returns immediately; the work happens on the GTK
// thread. windows must not be empty and its entries must not be nil.
func withNativeWindows(what string, windows []*application.WebviewWindow, apply func([]unsafe.Pointer)) {
	if len(windows) == 0 || apply == nil {
		return
	}
	for _, w := range windows {
		if w == nil {
			log.Printf("%s: missing window handle; skipped", what)
			return
		}
	}
	var attempt func(n int)
	attempt = func(n int) {
		application.InvokeAsync(func() {
			handles := make([]unsafe.Pointer, len(windows))
			ready := true
			for i, w := range windows {
				if w.NativeWindow() == nil {
					ready = false
					break
				}
				handles[i] = w.NativeWindow()
			}
			if ready {
				apply(handles)
				return
			}
			if n+1 >= nativeWindowRetryAttempts {
				log.Printf("%s: native window still not available after %d attempts (~%s); leaving the default behaviour",
					what, nativeWindowRetryAttempts, time.Duration(nativeWindowRetryAttempts)*nativeWindowRetryInterval)
				return
			}
			time.AfterFunc(nativeWindowRetryInterval, func() { attempt(n + 1) })
		})
	}
	attempt(0)
}
