package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/desktop/internal/hubmgr"
	"flowdrop/desktop/internal/network"
)

func TestOriginOf(t *testing.T) {
	cases := map[string]string{
		"http://192.168.1.5:8080":       "http://192.168.1.5:8080",
		"http://192.168.1.5:8080/path/": "http://192.168.1.5:8080",
		"http://[::1]:8080/":            "http://[::1]:8080",
		"wails://localhost/index.html":  "wails://localhost",
		"":                              "",
		"not a url":                     "",
	}
	for in, want := range cases {
		if got := originOf(in); got != want {
			t.Errorf("originOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartupSettingsPersistAndValidate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLOWDROP_DESKTOP_CONFIG", dir+"/desktop.json")
	got, path, warn := loadStartupSettings()
	if warn != nil {
		t.Logf("startup warning (acceptable on a machine without LAN IPv4): %v", warn)
	}
	if path != dir+"/desktop.json" {
		t.Fatalf("settings path = %q", path)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("detected settings are invalid: %v", err)
	}
	// The detected default must be persisted so the next start is stable.
	if _, found, err := appsettings.Load(path); err != nil || !found {
		t.Fatalf("Load after detection = found=%v err=%v", found, err)
	}
	second, secondPath, _ := loadStartupSettings()
	if secondPath != path {
		t.Fatalf("second load path = %q, want %q", secondPath, path)
	}
	if !reflect.DeepEqual(second, got) {
		t.Fatalf("second load = %+v, want the persisted %+v", second, got)
	}
}

func TestStartupSettingsFallsBackOnCorruptFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLOWDROP_DESKTOP_CONFIG", dir+"/desktop.json")
	if err := os.WriteFile(dir+"/desktop.json", []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, warn := loadStartupSettings()
	if warn == nil {
		t.Fatal("corrupt settings produced no warning")
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("fallback settings are invalid: %v", err)
	}
}

func TestServerOnlyAllowsHttpExternalURLs(t *testing.T) {
	// This mirrors the validation inside handleRawMessage. Keeping the rule in
	// one small function makes the security-relevant policy testable.
	allowed := []string{"http://example.com/", "https://example.com/a?b=c"}
	denied := []string{"file:///etc/passwd", "javascript:alert(1)", "wails://localhost/", "data:text/html,hi", "ftp://x/"}
	for _, raw := range allowed {
		if !isAllowedExternalURL(raw) {
			t.Errorf("isAllowedExternalURL(%q) = false, want true", raw)
		}
	}
	for _, raw := range denied {
		if isAllowedExternalURL(raw) {
			t.Errorf("isAllowedExternalURL(%q) = true, want false", raw)
		}
	}
}

func TestNetworkListNeverSuggestsVirtualOrLoopback(t *testing.T) {
	list, err := network.List()
	if err != nil {
		t.Skipf("no network interfaces: %v", err)
	}
	addr, reachable := network.DefaultBind(list)
	if reachable {
		iface, ok := network.Find(list, addr)
		if !ok {
			t.Fatalf("DefaultBind returned %q which is not in the list", addr)
		}
		if iface.Kind == network.KindVirtual || iface.Kind == network.KindLoopback {
			t.Fatalf("DefaultBind picked %q (%s); phones cannot reach it", addr, iface.Kind)
		}
	}
}

// TestStartupDualSettingsBindBothRealAddresses goes through the real startup
// path (desktop.json -> loadStartupSettings -> startHub) using this machine's
// LAN + Tailscale addresses. It is skipped when the machine has no such pair.
func TestStartupDualSettingsBindBothRealAddresses(t *testing.T) {
	list, err := network.List()
	if err != nil {
		t.Skipf("network list: %v", err)
	}
	lan, ts := dualModePicksForTest(list)
	if lan == "" {
		t.Skip("no real LAN + Tailscale pair on this machine")
	}
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "desktop.json")
	cfgPath := filepath.Join(dir, "hub-config.json")
	t.Setenv("FLOWDROP_DESKTOP_CONFIG", settingsPath)
	t.Setenv("FLOWDROP_DATA_DIR", filepath.Join(dir, "data"))
	t.Setenv("FLOWDROP_DISCOVERY_ENABLED", "false")
	port := freePortOnAddress(t, lan)
	raw, err := json.Marshal(appsettings.Settings{
		BindAddress:             lan,
		Port:                    port,
		AdditionalBindAddresses: []string{ts},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	got, path, warn := loadStartupSettings()
	if warn != nil {
		t.Fatalf("loadStartupSettings(dual) warning: %v", warn)
	}
	if path != settingsPath || !got.DualMode() || got.BindAddress != lan || got.AdditionalBindAddresses[0] != ts {
		t.Fatalf("loadStartupSettings(dual) = %+v path=%q", got, path)
	}

	m := hubmgr.New()
	if err := startHub(m, got, cfgPath); err != nil {
		t.Fatalf("startHub(dual): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	})
	urls := m.URLs()
	if len(urls) != 2 {
		t.Fatalf("startup dual URLs = %v, want LAN + Tailscale listeners", urls)
	}
	if !strings.Contains(urls[0], lan) || !strings.Contains(urls[1], ts) {
		t.Fatalf("startup dual URLs = %v, want %s then %s", urls, lan, ts)
	}
}
