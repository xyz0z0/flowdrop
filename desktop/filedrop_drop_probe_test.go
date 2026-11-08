//go:build linux && cgo && gtk3 && flowdrop_filedrop_probe

package main

import "testing"

// TestInboxFileDropClaimsCopyForOwnFileDrags pins the drag-action rule that the
// "drag-motion" handler uses. GTK's drag-motion uses the TRUE-handled
// accumulator and WebKit's own handler is connect_after, so only TRUE (plus our
// gdk_drag_status(COPY)) keeps WebKit from replacing the action with the page's
// drop operation. That must happen for the bridge's own drags and for nothing
// else, and never for an action the source does not offer.
func TestInboxFileDropClaimsCopyForOwnFileDrags(t *testing.T) {
	cases := []struct {
		name       string
		origin     bool
		fileDrag   bool
		offersCopy bool
		claim      bool
	}{
		{"hub origin, local file drag, source offers copy", true, true, true, true},
		{"no copy offered is never advertised", true, true, false, false},
		{"not a local file drag (text/html or no uri-list)", true, false, true, false},
		{"foreign origin keeps WebKit handling", false, true, true, false},
		{"ordinary text/url drag", false, false, false, false},
	}

	for _, tc := range cases {
		if got := probeShouldClaim(tc.origin, tc.fileDrag, tc.offersCopy); got != tc.claim {
			t.Errorf("%s: claim = %v, want %v", tc.name, got, tc.claim)
		}
	}
}

// TestInboxFileDropAuthorizationSurvivesLeave proves that a drop accepted by
// "drag-drop" still owns its data after "drag-leave": GTK emits leave before
// drag-drop and GDK Wayland can emit a separate leave while the file data is
// still travelling back from the source application.
func TestInboxFileDropAuthorizationSurvivesLeave(t *testing.T) {
	const timeoutUs = 5_000_000
	const ctx = 0x1000
	consume := probeDataCode(2)

	probeAuthReset()
	probeAuthAccept(ctx, 1000)
	probeAuthLeave()

	if got := probeAuthContext(); got != ctx {
		t.Fatalf("authorized context after leave = %#x, want %#x (leave must not revoke an accepted drop)", got, ctx)
	}
	if got := probeAuthData(ctx, 1000+timeoutUs-1, timeoutUs); got != consume {
		t.Fatalf("data after leave = %d, want %d (the accepted drop must still be served)", got, consume)
	}
	if got := probeAuthContext(); got != 0 {
		t.Fatalf("authorized context after consumption = %#x, want 0", got)
	}
}

// TestInboxFileDropRejectsForeignExpiredAndSpentData pins the three ways a
// "drag-data-received" must be left alone or dropped: another drag context
// (WebKit's hover read, a drag left to WebKit), data that arrives after the
// accepted drop's window, and a second delivery for an already consumed drop.
func TestInboxFileDropRejectsForeignExpiredAndSpentData(t *testing.T) {
	const timeoutUs = 5_000_000
	const accepted = 0x1000
	const foreign = 0x2000
	ignore := probeDataCode(0)
	expired := probeDataCode(1)
	consume := probeDataCode(2)

	// A foreign context never touches the pending authorization.
	probeAuthReset()
	probeAuthAccept(accepted, 1000)
	if got := probeAuthData(foreign, 1000, timeoutUs); got != ignore {
		t.Fatalf("data for a foreign context = %d, want %d", got, ignore)
	}
	if got := probeAuthContext(); got != accepted {
		t.Fatalf("foreign data changed the authorization: context = %#x, want %#x", got, accepted)
	}

	// The accepted drop itself still works, exactly once.
	if got := probeAuthData(accepted, 1000, timeoutUs); got != consume {
		t.Fatalf("data for the accepted context = %d, want %d", got, consume)
	}
	if got := probeAuthData(accepted, 1000, timeoutUs); got != ignore {
		t.Fatalf("second delivery for the same drop = %d, want %d", got, ignore)
	}

	// Past the data window the drop is dropped and its authorization cleared.
	probeAuthReset()
	probeAuthAccept(accepted, 1000)
	if got := probeAuthData(accepted, 1000+timeoutUs+1, timeoutUs); got != expired {
		t.Fatalf("late data = %d, want %d", got, expired)
	}
	if got := probeAuthContext(); got != 0 {
		t.Fatalf("authorized context after expiry = %#x, want 0", got)
	}

	// No authorization at all (page load, view destroy, new drop) means ignore.
	probeAuthReset()
	if got := probeAuthData(accepted, 1000, timeoutUs); got != ignore {
		t.Fatalf("data without authorization = %d, want %d", got, ignore)
	}
}

// TestInboxFileDropNewDropRevokesPreviousAuthorization pins that the next
// "drag-drop" replaces the authorization instead of adding one: a late delivery
// for the previous context must not be consumed by the new drop.
func TestInboxFileDropNewDropRevokesPreviousAuthorization(t *testing.T) {
	const timeoutUs = 5_000_000
	const first = 0x1000
	const second = 0x2000
	ignore := probeDataCode(0)
	consume := probeDataCode(2)

	probeAuthReset()
	probeAuthAccept(first, 1000)
	probeAuthAccept(second, 1100)

	if got := probeAuthData(first, 1200, timeoutUs); got != ignore {
		t.Fatalf("late data for the replaced drop = %d, want %d", got, ignore)
	}
	if got := probeAuthData(second, 1200, timeoutUs); got != consume {
		t.Fatalf("data for the current drop = %d, want %d", got, consume)
	}
}
