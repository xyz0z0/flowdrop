//go:build linux && cgo && gtk3 && flowdrop_filedrop_probe

package main

import "testing"

// TestInboxFileDropSignalMatching pins the exact GObject behaviour the inbox
// file-drop restore depends on, using a plain GObject and a test signal: no GTK
// init, no window, no display and no main loop.
//
// It guards a real regression: g_signal_handlers_{block,unblock,disconnect}_matched
// take (instance, mask, signal_id, detail, closure, func, data), so the Wails
// window id must be the last argument. Passing it in the func slot matched
// nothing, which reported "already active" without removing anything.
func TestInboxFileDropSignalMatching(t *testing.T) {
	const (
		target   = uintptr(4242) // stands in for the Wails window id
		other    = uintptr(7777)
		absent   = uintptr(9999)
		cbTarget = 0
		cbOther  = 1
	)

	probeResetHits()
	object := probeNewObject()
	if object == 0 {
		t.Fatal("could not create the probe GObject")
	}
	defer probeUnref(object)

	probeConnect(object, target, cbTarget)
	probeConnect(object, other, cbOther)

	// Both handlers are live to begin with.
	probeEmit(object)
	if got := probeHits(cbTarget); got != 1 {
		t.Fatalf("target handler fired %d times before counting, want 1", got)
	}
	if got := probeHits(cbOther); got != 1 {
		t.Fatalf("other handler fired %d times before counting, want 1", got)
	}

	// Exactly the target handler matches; zero matches are distinguishable.
	if got := probeMatchCount(object, target); got != 1 {
		t.Fatalf("match count for the target user_data = %d, want 1 (wrong argument order matches nothing)", got)
	}
	if got := probeMatchCount(object, other); got != 1 {
		t.Fatalf("match count for the other user_data = %d, want 1", got)
	}
	if got := probeMatchCount(object, absent); got != 0 {
		t.Fatalf("match count for an unused user_data = %d, want 0", got)
	}

	// Counting must not leave any handler blocked: both fire again.
	probeEmit(object)
	if got := probeHits(cbTarget); got != 2 {
		t.Fatalf("target handler fired %d times after counting, want 2 (handler left blocked)", got)
	}
	if got := probeHits(cbOther); got != 2 {
		t.Fatalf("other handler fired %d times after counting, want 2 (handler left blocked)", got)
	}

	// Disconnecting the target removes exactly one handler and keeps the other.
	if got := probeDisconnect(object, target); got != 1 {
		t.Fatalf("disconnect of the target user_data removed %d handlers, want 1", got)
	}
	if got := probeMatchCount(object, target); got != 0 {
		t.Fatalf("match count for the disconnected target = %d, want 0", got)
	}
	if got := probeMatchCount(object, other); got != 1 {
		t.Fatalf("match count for the untouched user_data = %d, want 1", got)
	}
	probeEmit(object)
	if got := probeHits(cbTarget); got != 2 {
		t.Fatalf("disconnected target handler fired %d times, want 2 (unchanged)", got)
	}
	if got := probeHits(cbOther); got != 3 {
		t.Fatalf("other handler fired %d times after disconnect, want 3", got)
	}

	// Two handlers sharing one user_data value are reported as ambiguous, which
	// is exactly why flowdrop_relax_inbox_dnd insists on exactly one match.
	probeConnect(object, absent, cbTarget)
	probeConnect(object, absent, cbTarget)
	if got := probeMatchCount(object, absent); got != 2 {
		t.Fatalf("match count for a duplicated user_data = %d, want 2", got)
	}
	if got := probeDisconnect(object, absent); got != 2 {
		t.Fatalf("disconnect for a duplicated user_data removed %d handlers, want 2", got)
	}
	if got := probeMatchCount(object, absent); got != 0 {
		t.Fatalf("match count after removing the duplicates = %d, want 0", got)
	}
}
