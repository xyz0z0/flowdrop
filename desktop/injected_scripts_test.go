package main

import (
	"strings"
	"testing"
)

// TestJoinInjectedScriptsIsolatesFailures pins the rule that one snippet
// throwing at top level must not stop the ones after it: Wails runs the whole
// string as a single script, so an uncaught exception would otherwise kill
// every later statement (the settings entry and the login bootstrap were lost
// that way in the container run of 2026-11-09).
func TestJoinInjectedScriptsIsolatesFailures(t *testing.T) {
	js := joinInjectedScripts("first();", "", "second();")
	if strings.Count(js, "flowdrop: isolated snippet") != 2 {
		t.Fatalf("each non-empty snippet must be wrapped in try/catch, got:\n%s", js)
	}
	if strings.Contains(js, "try {\n\n}") {
		t.Fatalf("the empty snippet must be skipped, got:\n%s", js)
	}
	if strings.Index(js, "first();") > strings.Index(js, "second();") {
		t.Fatalf("snippet order must be preserved, got:\n%s", js)
	}
	if joinInjectedScripts("", "") != "" {
		t.Fatalf("an all-empty script set must be empty")
	}
}

// TestInboxInjectedJSBootstrapsLoginFirst guards the login path: the bootstrap
// is the only snippet the page cannot do without, and it has to run before the
// chrome/entry/guard snippets that may touch the page.
func TestInboxInjectedJSBootstrapsLoginFirst(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ" // 43 base64url chars
	js := inboxInjectedJS(token)
	boot := strings.Index(js, "__flowdropDesktopBootstrap")
	chrome := strings.Index(js, "__flowdropDesktopChrome")
	entry := strings.Index(js, "__flowdropDesktopSettingsEntry")
	if boot < 0 || chrome < 0 || entry < 0 {
		t.Fatalf("missing snippet markers: boot=%d chrome=%d entry=%d", boot, chrome, entry)
	}
	if !(boot < chrome && boot < entry) {
		t.Fatalf("bootstrap must come before chrome and the settings entry (boot=%d chrome=%d entry=%d)", boot, chrome, entry)
	}
	if strings.Count(js, "flowdrop: isolated snippet") != 5 {
		t.Fatalf("expected five isolated snippets (bootstrap + four page scripts), got %d", strings.Count(js, "flowdrop: isolated snippet"))
	}
	if strings.Count(js, token) != 1 {
		t.Fatalf("the device token must appear exactly once (inside the bootstrap)")
	}

	unpaired := inboxInjectedJS("")
	if strings.Contains(unpaired, "__flowdropDesktopBootstrap") {
		t.Fatalf("an unpaired shell must inject no bootstrap")
	}
	if strings.Count(unpaired, "flowdrop: isolated snippet") != 4 {
		t.Fatalf("expected four isolated snippets without a token, got %d", strings.Count(unpaired, "flowdrop: isolated snippet"))
	}
}
