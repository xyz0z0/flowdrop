package main

import (
	"strings"
	"testing"
)

// The injected bar must paint itself with the Hub page's own colour tokens:
// with a hard-coded white it stopped matching the page below it (the window
// read as two stacked surfaces instead of one).
func TestInjectedTitlebarUsesPageTokens(t *testing.T) {
	for _, want := range []string{
		"background: var(--bg,",
		"color: var(--fg,",
		"border-bottom: 1px solid var(--line-2,",
		"height: var(--flowdrop-chrome-top)",
		"padding-top: var(--flowdrop-chrome-top)",
	} {
		if !strings.Contains(desktopWindowChromeCSS, want) {
			t.Errorf("desktopWindowChromeCSS is missing %q", want)
		}
	}
	for _, forbidden := range []string{"background: #ffffff", "background: #232327"} {
		if strings.Contains(desktopWindowChromeCSS, forbidden) {
			t.Errorf("desktopWindowChromeCSS still hard-codes %q", forbidden)
		}
	}
}

// web/src/styles.css subtracts this exact custom property from its viewport
// height, so the pairing screen fits under the bar instead of overflowing into
// a scrollbar. Renaming one side breaks the other.
func TestInjectedTitlebarHeightIsSharedWithWebUI(t *testing.T) {
	if !strings.Contains(desktopWindowChromeCSS, "--flowdrop-chrome-top: 36px") {
		t.Fatal("desktopWindowChromeCSS must define --flowdrop-chrome-top with the bar's height")
	}
	if !strings.Contains(desktopWindowChromeCSS, "height: var(--flowdrop-chrome-top)") {
		t.Fatal("the bar's own height must come from --flowdrop-chrome-top")
	}
}
