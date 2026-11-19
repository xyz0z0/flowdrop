package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"

	"flowdrop/internal/webui"
)

func TestInboxControlActionAllowsOnlyFixedSettingsFromHubOrigin(t *testing.T) {
	d := &desktopState{hubOrigin: "http://192.168.1.10:8080"}
	good := &application.OriginInfo{Origin: "http://192.168.1.10:8080/"}
	bad := &application.OriginInfo{Origin: "http://192.168.1.10:8081/"}

	if action, _ := d.inboxControlAction(true, rawOpenSettingsMessage, good); action != inboxActionOpenSettings {
		t.Fatalf("settings action = %v, want inboxActionOpenSettings", action)
	}
	for name, tc := range map[string]struct {
		isInbox bool
		msg     string
		origin  *application.OriginInfo
	}{
		"settings window is not the inbox": {false, rawOpenSettingsMessage, good},
		"different origin":                 {true, rawOpenSettingsMessage, bad},
		"different host":                   {true, rawOpenSettingsMessage, &application.OriginInfo{Origin: "http://192.168.1.11:8080/"}},
		"different scheme":                 {true, rawOpenSettingsMessage, &application.OriginInfo{Origin: "https://192.168.1.10:8080/"}},
		"missing origin":                   {true, rawOpenSettingsMessage, nil},
		"unknown native action":            {true, "flowdrop:setproxy:whatever", good},
		"wails reserved message":           {true, "wails:runtime:call", good},
		"settings with extra payload":      {true, rawOpenSettingsMessage + ":extra", good},
	} {
		t.Run(name, func(t *testing.T) {
			if action, target := d.inboxControlAction(tc.isInbox, tc.msg, tc.origin); action != inboxActionNone || target != "" {
				t.Fatalf("action=%v target=%q, want none", action, target)
			}
		})
	}

	// An empty/unknown Hub origin fails closed, even for the right window.
	closed := &desktopState{}
	if action, _ := closed.inboxControlAction(true, rawOpenSettingsMessage, good); action != inboxActionNone {
		t.Fatal("message accepted without a known Hub origin")
	}
}

func TestInboxControlActionKeepsExternalURLLimited(t *testing.T) {
	d := &desktopState{hubOrigin: "http://127.0.0.1:8080"}
	origin := &application.OriginInfo{Origin: "http://127.0.0.1:8080/"}
	action, target := d.inboxControlAction(true, rawOpenURLPrefix+"https://example.com/a?b=c", origin)
	if action != inboxActionOpenExternal || target != "https://example.com/a?b=c" {
		t.Fatalf("external action = %v %q", action, target)
	}
	for _, msg := range []string{
		rawOpenURLPrefix + "javascript:alert(1)",
		rawOpenURLPrefix + "file:///etc/passwd",
		rawOpenURLPrefix + "wails://localhost/index.html",
		rawOpenURLPrefix + strings.Repeat("a", 5000),
		rawOpenURLPrefix,
	} {
		if action, _ := d.inboxControlAction(true, msg, origin); action != inboxActionNone {
			t.Fatalf("%q was accepted as an external URL", msg)
		}
	}
}

func TestSettingsEntryScriptUsesOnlyTheFixedRawMessage(t *testing.T) {
	for _, want := range []string{
		`flowdrop:opensettings`,
		`MutationObserver`,
		`.topbar-right`,
		`"设置"`,
		`Ctrl+,`,
	} {
		if !strings.Contains(desktopSettingsEntryJS, want) {
			t.Errorf("settings entry script is missing %q", want)
		}
	}
	// The injected entry must not become a generic native bridge or a second
	// URL channel; those would bypass the origin guard's closed action set.
	for _, forbidden := range []string{"Call.ByName", "wails/runtime", "flowdrop:openurl"} {
		if strings.Contains(desktopSettingsEntryJS, forbidden) {
			t.Errorf("settings entry script must not contain %q", forbidden)
		}
	}
}

func TestSettingsEntryButtonIsDesktopOnly(t *testing.T) {
	// The desktop entry is injected only through WebviewWindowOptions.JS. The
	// local settings asset must not carry it, and neither may the Hub's React
	// build that LAN browsers download.
	local, err := fs.ReadFile(mustSub(assets), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(local), "flowdrop-desktop-settings") || strings.Contains(string(local), rawOpenSettingsMessage) {
		t.Fatal("local settings asset unexpectedly contains the inbox settings entry")
	}

	hubFS := webui.FS()
	if hubFS == nil {
		t.Skip("web/dist is not built; run npm run build in web/ to enable this check")
	}
	checked := 0
	werr := fs.WalkDir(hubFS, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		switch {
		case strings.HasSuffix(path, ".html"), strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".css"):
		default:
			return nil
		}
		b, err := fs.ReadFile(hubFS, path)
		if err != nil {
			return err
		}
		checked++
		if strings.Contains(string(b), "flowdrop-desktop-settings") || strings.Contains(string(b), rawOpenSettingsMessage) {
			t.Errorf("Hub browser asset %s contains the desktop-only settings entry", path)
		}
		return nil
	})
	if werr != nil {
		t.Fatal(werr)
	}
	if checked == 0 {
		t.Skip("no built Hub assets found; run npm run build in web/ to enable this check")
	}
}

func TestCombineProxyWarningHonorsInAppDirectMode(t *testing.T) {
	const environment = "系统检测到代理风险；若未豁免该地址，WebSocket 可能被改写而离线。请把 192.168.1.10 加入 no_proxy。"

	if got := combineProxyWarning(environment, true, ""); got != "" {
		t.Fatalf("applied in-app direct mode still warned: %q", got)
	}
	if got := combineProxyWarning(environment, true, "should not matter"); got != "" {
		t.Fatalf("applied in-app direct mode with stale error still warned: %q", got)
	}
	if got := combineProxyWarning(environment, false, ""); got != environment {
		t.Fatalf("unapplied in-app mode changed the environment diagnosis: %q", got)
	}
	got := combineProxyWarning(environment, false, "WebKitGTK default data manager unavailable")
	if !strings.Contains(got, "应用内 WebView 直连代理设置失败") || !strings.Contains(got, environment) {
		t.Fatalf("failed in-app mode must keep the real diagnosis, got %q", got)
	}
	// A failed in-app bypass is not a warning by itself on a machine where the
	// host has no proxy risk to bypass.
	if got := combineProxyWarning("", false, "boom"); got != "" {
		t.Fatalf("no environment proxy risk produced a warning: %q", got)
	}
}
