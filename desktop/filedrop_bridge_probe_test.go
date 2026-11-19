//go:build linux && cgo && gtk3 && flowdrop_filedrop_probe

package main

import (
	"strings"
	"testing"
)

// TestInboxFileChooserDecision pins who owns a run-file-chooser request: only
// the exact current per-drop MIME token may be answered with the pending native
// files; a token from another (earlier/expired) drop batch must be cancelled;
// and any ordinary page file input must keep WebKit's default dialog.
func TestInboxFileChooserDecision(t *testing.T) {
	prefix := probeDropTokenPrefix()
	chooserDefault := probeChooserCode(0)
	chooserSelect := probeChooserCode(1)
	chooserCancel := probeChooserCode(2)
	current := prefix + "0123456789abcdef"
	stale := prefix + "fedcba9876543210"

	cases := []struct {
		name   string
		mimes  []string
		token  string
		expect int
	}{
		{"current token exact", []string{current}, current, chooserSelect},
		{"current token among others", []string{"text/plain", current}, current, chooserSelect},
		{"ordinary input, no token", []string{"image/png"}, "", chooserDefault},
		{"ordinary input, no token, no mime", nil, "", chooserDefault},
		{"ordinary input while a drop is pending", []string{"image/png"}, current, chooserDefault},
		{"stale drop token", []string{stale}, current, chooserCancel},
		{"stale drop token without current batch", []string{stale}, "", chooserCancel},
		{"prefix without random part", []string{prefix}, current, chooserCancel},
		{"current wins over stale", []string{stale, current}, current, chooserSelect},
	}

	for _, tc := range cases {
		if got := probeChooserDecision(tc.mimes, tc.token); got != tc.expect {
			t.Errorf("%s: decision = %d, want %d", tc.name, got, tc.expect)
		}
	}
}

// TestInboxFileDropTokenIsScopedMime guards the shape the chooser matching
// depends on: an ASCII "application/x-flowdrop-drop-<hex>" marker, which is
// also a syntactically valid MIME type for input.accept.
func TestInboxFileDropTokenIsScopedMime(t *testing.T) {
	if got := probeDropTokenPrefix(); !strings.Contains(got, "application/x-flowdrop-drop-") {
		t.Fatalf("drop token prefix = %q, want the documented application/x-flowdrop-drop- prefix", got)
	}
}
