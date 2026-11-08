package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/desktop/internal/hubmgr"
	"flowdrop/desktop/internal/network"
)

// newTestService builds the settings service against a real in-process Hub using
// a temporary data directory, with no Wails application attached (the GUI parts
// are nil-guarded, exactly as they are before the app finishes starting).
// freePort reserves an ephemeral loopback port for the test Hub.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func newTestService(t *testing.T) (*SettingsService, *hubmgr.Manager, int) {
	t.Helper()
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "FLOWDROP_") && key != "FLOWDROP_DESKTOP_CONFIG" {
			old, had := os.LookupEnv(key)
			if err := os.Unsetenv(key); err != nil {
				t.Fatalf("unset %s: %v", key, err)
			}
			t.Cleanup(func() {
				if had {
					os.Setenv(key, old)
				}
			})
		}
	}
	dir := t.TempDir()
	t.Setenv("FLOWDROP_DESKTOP_CONFIG", filepath.Join(dir, "desktop.json"))
	t.Setenv("FLOWDROP_DATA_DIR", filepath.Join(dir, "data"))
	// Tests must not publish mDNS on the developer's real LAN.
	t.Setenv("FLOWDROP_DISCOVERY_ENABLED", "false")

	m := hubmgr.New()
	cfgPath := filepath.Join(dir, "hub-config.json")
	if err := m.Start(hubmgr.Config{
		ConfigPath:  cfgPath,
		BindAddress: "127.0.0.1",
		Port:        freePort(t),
	}); err != nil {
		t.Fatalf("start test hub: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = m.Stop(ctx)
	})
	desktop.settingsPath = os.Getenv("FLOWDROP_DESKTOP_CONFIG")
	desktop.hubConfigPath = cfgPath
	desktop.lastStartupErr = ""
	desktop.app = nil
	desktop.inbox = nil
	desktop.settings = nil

	return newSettingsService(m), m, 0
}

func TestServiceStateReflectsHub(t *testing.T) {
	svc, m, _ := newTestService(t)
	st, err := svc.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !st.Running || st.URL != m.URL() {
		t.Fatalf("GetState = %+v, want running at %s", st, m.URL())
	}
	if st.AdminConfigured {
		t.Fatal("AdminConfigured = true on a fresh data dir")
	}
	if len(st.Interfaces) == 0 {
		t.Fatal("Interfaces is empty; the settings window needs real choices")
	}
	for _, i := range st.Interfaces {
		if i.Address == "" || i.Label == "" || i.Kind == "" {
			t.Fatalf("interface entry %+v is incomplete", i)
		}
	}
}

func TestServiceInitializeAdminCannotOverwrite(t *testing.T) {
	svc, _, _ := newTestService(t)
	if _, err := svc.InitializeAdmin("admin", "short"); err == nil {
		t.Fatal("InitializeAdmin accepted a short password")
	}
	st, err := svc.InitializeAdmin("admin", "correct-horse-battery")
	if err != nil {
		t.Fatalf("InitializeAdmin: %v", err)
	}
	if !st.AdminConfigured {
		t.Fatal("AdminConfigured = false after InitializeAdmin")
	}
	_, err = svc.InitializeAdmin("admin", "another-password-123")
	if err == nil || !strings.Contains(err.Error(), "已经设置过管理员密码") {
		t.Fatalf("second InitializeAdmin = %v, want the no-overwrite message", err)
	}
}

func TestServiceApplyNetworkSwitchesAndPersists(t *testing.T) {
	svc, m, _ := newTestService(t)
	before := m.URL()

	nextPort := freePort(t)
	st, err := svc.ApplyNetwork("127.0.0.1", nextPort)
	if err != nil {
		t.Fatalf("ApplyNetwork: %v", err)
	}
	if !st.Running || st.URL == before || st.URL == "" {
		t.Fatalf("ApplyNetwork = %+v (before %s), want a new running URL", st, before)
	}
	if st.Port != nextPort || st.BindAddress != "127.0.0.1" {
		t.Fatalf("ApplyNetwork state = %+v, want 127.0.0.1:%d", st, nextPort)
	}
	// The choice must be persisted for the next start.
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")
	if savedPath == "" {
		t.Fatal("test did not set FLOWDROP_DESKTOP_CONFIG")
	}
	raw, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("read persisted settings: %v", err)
	}
	if !strings.Contains(string(raw), `"port": `+itoa(nextPort)) {
		t.Fatalf("persisted settings do not contain the new port: %s", raw)
	}
}

func TestServiceApplyNetworkRejectsWildcardAndUnknownIP(t *testing.T) {
	svc, m, _ := newTestService(t)
	before := m.URL()

	if _, err := svc.ApplyNetwork("0.0.0.0", 8080); err == nil || !strings.Contains(err.Error(), "wildcard") {
		t.Fatalf("ApplyNetwork(0.0.0.0) = %v, want wildcard rejection", err)
	}
	if _, err := svc.ApplyNetwork("300.1.2.3", 8080); err == nil {
		t.Fatal("ApplyNetwork accepted an invalid IP")
	}
	// An address that does not exist on this machine must be refused before the
	// old Hub is stopped, so the app keeps working.
	if _, err := svc.ApplyNetwork("192.0.2.123", 8080); err == nil || !strings.Contains(err.Error(), "没有") {
		t.Fatalf("ApplyNetwork(absent IP) = %v, want a clear refusal", err)
	}
	if m.URL() != before {
		t.Fatalf("Hub URL changed after rejected settings: %s -> %s", before, m.URL())
	}
}

func httpClientForTest() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestServiceApplyNetworkRollbackRestoresPreviousHub proves a failed rebind
// (port taken) leaves the user with the previous working Hub instead of a
// silently stopped or half-started one.
func TestServiceApplyNetworkRollbackRestoresPreviousHub(t *testing.T) {
	svc, m, _ := newTestService(t)
	before := m.URL()
	if before == "" {
		t.Fatal("test Hub has no URL")
	}

	// Hold a port so the new Hub cannot bind it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	takenPort := ln.Addr().(*net.TCPAddr).Port
	defer ln.Close()

	_, err = svc.ApplyNetwork("127.0.0.1", takenPort)
	if err == nil || !strings.Contains(err.Error(), "端口已被占用") {
		t.Fatalf("ApplyNetwork(occupied port) = %v, want a clear port-in-use error", err)
	}
	if got := m.URL(); got != before {
		t.Fatalf("Hub URL after failed apply = %q, want the restored %q", got, before)
	}
	// The restored Hub must actually serve again, not just report a URL.
	c := httpClientForTest()
	res, err := c.Get(before + "/api/v1/health")
	if err != nil {
		t.Fatalf("health after rollback: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("health after rollback = %d, want 200", res.StatusCode)
	}
	// The failed choice must not have been persisted.
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")
	if savedPath != "" {
		if saved, found, lerr := appsettings.Load(savedPath); lerr == nil && found && saved.Port == takenPort {
			t.Fatalf("failed settings were persisted: %+v", saved)
		}
	}
}

// TestServiceApplyDualNetworkUsesRealLANAndTailscale runs against this machine's
// real LAN and Tailscale addresses when both exist. It verifies the settings
// layer starts one Hub on both addresses, reports both actual URLs with their
// kinds, and persists the dual configuration. It says nothing about a phone on
// another network (that remains unverified).
func TestServiceApplyDualNetworkUsesRealLANAndTailscale(t *testing.T) {
	list, err := network.List()
	if err != nil {
		t.Skipf("network list: %v", err)
	}
	lan, _ := dualModePicksForTest(list)
	if lan == "" {
		t.Skip("no real LAN + Tailscale pair on this machine")
	}
	var ts string
	for _, i := range list {
		if i.Kind == network.KindTailscale {
			ts = i.Address
			break
		}
	}

	svc, m, _ := newTestService(t)
	port := freePortOnAddress(t, lan)
	st, err := svc.ApplyDualNetwork(lan, ts, port)
	if err != nil {
		t.Fatalf("ApplyDualNetwork(%s,%s,%d): %v", lan, ts, port, err)
	}
	if !st.Running || !st.DualMode || st.BindAddress != lan || len(st.Additional) != 1 || st.Additional[0] != ts {
		t.Fatalf("dual state = %+v", st)
	}
	if len(st.AccessURLs) != 2 {
		t.Fatalf("AccessURLs = %+v, want the two actual listeners", st.AccessURLs)
	}
	kinds := map[string]string{}
	for _, u := range st.AccessURLs {
		kinds[u.Address] = u.Kind
	}
	if kinds[lan] != string(network.KindLAN) || kinds[ts] != string(network.KindTailscale) {
		t.Fatalf("AccessURLs kinds = %v, want LAN+Tailscale", kinds)
	}
	urls := m.URLs()
	if len(urls) != 2 || st.URL != urls[0] {
		t.Fatalf("manager URLs = %v, state URL = %q", urls, st.URL)
	}
	for _, base := range urls {
		res, err := httpClientForTest().Get(base + "/api/v1/health")
		if err != nil {
			t.Fatalf("health %s: %v", base, err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("health %s = %d", base, res.StatusCode)
		}
	}

	saved, found, err := appsettings.Load(os.Getenv("FLOWDROP_DESKTOP_CONFIG"))
	if err != nil || !found {
		t.Fatalf("persisted settings = found=%v err=%v", found, err)
	}
	if !saved.DualMode() || saved.BindAddress != lan || saved.AdditionalBindAddresses[0] != ts || saved.Port != port {
		t.Fatalf("persisted dual settings = %+v", saved)
	}

	// A failed re-bind must restore the previous dual configuration, not leave
	// the app with zero or one listener.
	held, err := net.Listen("tcp", net.JoinHostPort(lan, "0"))
	if err != nil {
		t.Fatalf("occupy a LAN port: %v", err)
	}
	defer held.Close()
	takenPort := held.Addr().(*net.TCPAddr).Port
	if _, err := svc.ApplyDualNetwork(lan, ts, takenPort); err == nil || !strings.Contains(err.Error(), "端口已被占用") {
		t.Fatalf("ApplyDualNetwork(occupied port) = %v, want port-in-use error", err)
	}
	restored := m.URLs()
	if len(restored) != 2 || m.URL() != urls[0] {
		t.Fatalf("URLs after failed dual re-bind = %v (URL %q), want the previous two listeners", restored, m.URL())
	}
	for _, base := range restored {
		res, err := httpClientForTest().Get(base + "/api/v1/health")
		if err != nil {
			t.Fatalf("health after dual rollback %s: %v", base, err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("health after dual rollback %s = %d", base, res.StatusCode)
		}
	}
	// The failed port must not have replaced the persisted configuration.
	after, found, err := appsettings.Load(os.Getenv("FLOWDROP_DESKTOP_CONFIG"))
	if err != nil || !found || after.Port != port || !after.DualMode() {
		t.Fatalf("persisted settings after failed re-bind = %+v found=%v err=%v", after, found, err)
	}
}

func TestServiceApplyNetworkConfigRejectsBadDual(t *testing.T) {
	svc, m, _ := newTestService(t)
	before := m.URL()
	// Loopback cannot be the primary LAN listener of dual mode.
	if _, err := svc.ApplyDualNetwork("127.0.0.1", "100.64.0.1", 8080); err == nil || !strings.Contains(err.Error(), "内网") {
		t.Fatalf("ApplyDualNetwork(loopback primary) = %v, want a LAN-primary refusal", err)
	}
	if m.URL() != before {
		t.Fatalf("Hub changed after a rejected dual request: %s -> %s", before, m.URL())
	}
}

func dualModePicksForTest(list []network.Interface) (lan, tailscale string) {
	for _, i := range list {
		if i.Kind == network.KindLAN && lan == "" {
			lan = i.Address
		}
		if i.Kind == network.KindTailscale && tailscale == "" {
			tailscale = i.Address
		}
	}
	if lan == "" || tailscale == "" {
		return "", ""
	}
	return lan, tailscale
}

func freePortOnAddress(t *testing.T, host string) int {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatalf("reserve free port on %s: %v", host, err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}
