package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	// main() hands the same manager to both the state and the service; mirror
	// that so inboxBaseURL() resolves against the test Hub.
	desktop.manager = m
	desktop.setStartupError("")
	desktop.setRemoteHubURL("")
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
	if len(st.Interfaces) == 0 {
		t.Fatal("Interfaces is empty; the settings window needs real choices")
	}
	for _, i := range st.Interfaces {
		if i.Address == "" || i.Label == "" || i.Kind == "" {
			t.Fatalf("interface entry %+v is incomplete", i)
		}
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

// TestServiceApplyNetworkKeepsDeviceIdentity covers the regression where saving
// a new bind address rebuilt desktop.json from scratch and wiped the device ID
// and token the shell had paired with, so the next launch paired again and left
// a dead device row in the Hub.
func TestServiceApplyNetworkKeepsDeviceIdentity(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := appsettings.Save(desktop.settingsPath, appsettings.Settings{
		Mode:               appsettings.ModeLocal,
		BindAddress:        "127.0.0.1",
		Port:               freePort(t),
		DesktopDeviceID:    "dev_local",
		DesktopDeviceToken: testDeviceToken,
	}); err != nil {
		t.Fatal(err)
	}

	nextPort := freePort(t)
	if _, err := svc.ApplyNetwork("127.0.0.1", nextPort); err != nil {
		t.Fatalf("ApplyNetwork: %v", err)
	}

	saved, found, err := appsettings.Load(desktop.settingsPath)
	if err != nil || !found {
		t.Fatalf("reload saved settings: found=%v err=%v", found, err)
	}
	if saved.Port != nextPort {
		t.Fatalf("saved port = %d, want %d", saved.Port, nextPort)
	}
	if saved.DesktopDeviceID != "dev_local" || saved.DesktopDeviceToken != testDeviceToken {
		t.Fatalf("saved device identity = %q/%q, want the paired one to survive",
			saved.DesktopDeviceID, saved.DesktopDeviceToken)
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

// RMT-03: ApplyMode switches the run mode of the running process immediately.
// local -> remote really stops the in-process Hub and points every URL consumer
// at the remote address; remote -> local really starts it again. No restart is
// needed and the response describes the new mode right away.
func TestServiceApplyModeSwitchesAtRuntime(t *testing.T) {
	svc, m, _ := newTestService(t)
	localURL := m.URL()
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")

	// The process already runs local mode, so saving local again is a no-op that
	// must not restart the Hub.
	st, err := svc.ApplyMode(appsettings.ModeLocal, "")
	if err != nil {
		t.Fatalf("ApplyMode(local): %v", err)
	}
	if st.Mode != appsettings.ModeLocal || st.RemoteURL != "" {
		t.Fatalf("ApplyMode(local) = %+v, want local mode", st)
	}
	if got := m.URL(); got != localURL {
		t.Fatalf("idempotent ApplyMode(local) restarted the Hub: %s -> %s", localURL, got)
	}

	// local -> remote: the local Hub is stopped here and now.
	remoteURL := "http://192.168.31.184:8080"
	st, err = svc.ApplyMode(appsettings.ModeRemote, remoteURL+"/")
	if err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	if st.Mode != appsettings.ModeRemote || st.RemoteURL != remoteURL {
		t.Fatalf("ApplyMode(remote) state = %+v, want mode=remote url=%s", st, remoteURL)
	}
	if st.Running || st.URL != "" {
		t.Fatalf("ApplyMode(remote) state = %+v, want the local Hub reported as stopped", st)
	}
	if got := m.URL(); got != "" {
		t.Fatalf("local Hub still running at %q after switching to remote", got)
	}
	// The tray, the native menu and OpenInBrowser all read this dynamically, so
	// it must already be the new address instead of a cached local one.
	if got := desktop.remoteHubURL(); got != remoteURL {
		t.Fatalf("runtime remote URL = %q, want %q", got, remoteURL)
	}
	if got := desktop.inboxBaseURL(); got != remoteURL {
		t.Fatalf("inboxBaseURL = %q, want the remote %q", got, remoteURL)
	}
	// The released port must be free again and the old origin must not answer.
	ln, err := net.Listen("tcp", strings.TrimPrefix(localURL, "http://"))
	if err != nil {
		t.Fatalf("port of the stopped Hub is still bound: %v", err)
	}
	_ = ln.Close()
	if res, err := httpClientForTest().Get(localURL + "/api/v1/health"); err == nil {
		res.Body.Close()
		t.Fatalf("stopped Hub still answers on %s", localURL)
	}

	saved, found, err := appsettings.Load(savedPath)
	if err != nil || !found {
		t.Fatalf("persisted settings = found=%v err=%v", found, err)
	}
	if saved.Mode != appsettings.ModeRemote || saved.RemoteURL != remoteURL {
		t.Fatalf("persisted settings = %+v, want remote mode and the trimmed URL", saved)
	}
	// Switching modes must keep the listen selection the user already had.
	if saved.BindAddress != "127.0.0.1" || saved.Port == 0 {
		t.Fatalf("persisted settings lost the listen selection: %+v", saved)
	}

	// remote -> local: the Hub is started from the saved listen selection.
	st, err = svc.ApplyMode(appsettings.ModeLocal, "")
	if err != nil {
		t.Fatalf("ApplyMode(local): %v", err)
	}
	if st.Mode != appsettings.ModeLocal || st.RemoteURL != "" {
		t.Fatalf("ApplyMode(local) state = %+v, want local mode", st)
	}
	if !st.Running || st.URL == "" {
		t.Fatalf("ApplyMode(local) state = %+v, want the Hub reported as running", st)
	}
	if got := desktop.remoteHubURL(); got != "" {
		t.Fatalf("runtime remote URL = %q after switching to local, want empty", got)
	}
	if got := desktop.inboxBaseURL(); got != m.URL() {
		t.Fatalf("inboxBaseURL = %q, want the new local %q", got, m.URL())
	}
	res, err := httpClientForTest().Get(m.URL() + "/api/v1/health")
	if err != nil {
		t.Fatalf("health after switching back to local: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("health after switching back to local = %d, want 200", res.StatusCode)
	}
	saved, found, err = appsettings.Load(savedPath)
	if err != nil || !found || saved.Mode != appsettings.ModeLocal || saved.RemoteURL != "" {
		t.Fatalf("persisted settings after switching back = %+v found=%v err=%v", saved, found, err)
	}

	// A missing settings file must be written again without touching the Hub.
	if err := os.Remove(savedPath); err != nil {
		t.Fatalf("remove settings: %v", err)
	}
	running := m.URL()
	if _, err := svc.ApplyMode(appsettings.ModeLocal, ""); err != nil {
		t.Fatalf("ApplyMode(local) without a settings file: %v", err)
	}
	if got := m.URL(); got != running {
		t.Fatalf("idempotent ApplyMode restarted the Hub: %s -> %s", running, got)
	}
	if _, found, err := appsettings.Load(savedPath); err != nil || !found {
		t.Fatalf("idempotent ApplyMode did not save the settings: found=%v err=%v", found, err)
	}
}

// TestServiceApplyModeRejectsInvalidInput proves the bridge reuses the settings
// validation and leaves both the file and the running mode untouched.
func TestServiceApplyModeRejectsInvalidInput(t *testing.T) {
	svc, m, _ := newTestService(t)
	remoteURL := "http://192.168.31.184:8080"
	if _, err := svc.ApplyMode(appsettings.ModeRemote, remoteURL); err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")

	cases := map[string][2]string{
		"unknown mode":              {"cloud", ""},
		"remote without an address": {appsettings.ModeRemote, ""},
		"remote without a scheme":   {appsettings.ModeRemote, "192.168.31.184:8080"},
		"remote with a path":        {appsettings.ModeRemote, "http://192.168.31.184:8080/hub"},
	}
	for name, in := range cases {
		if _, err := svc.ApplyMode(in[0], in[1]); err == nil {
			t.Fatalf("ApplyMode(%s) = nil error, want a validation error", name)
		}
	}
	after, found, err := appsettings.Load(savedPath)
	if err != nil || !found || after.Mode != appsettings.ModeRemote || after.RemoteURL != remoteURL {
		t.Fatalf("rejected ApplyMode changed the settings: %+v found=%v err=%v", after, found, err)
	}
	if got := desktop.remoteHubURL(); got != remoteURL {
		t.Fatalf("rejected ApplyMode changed the runtime mode: %q, want %q", got, remoteURL)
	}
	if got := m.URL(); got != "" {
		t.Fatalf("rejected ApplyMode started a local Hub at %q", got)
	}
}

// TestServiceApplyModeRemoteToLocalStartFailure covers a busy port: the switch
// must fail with a readable message and leave the app in its working remote mode
// instead of persisting a half-applied state.
func TestServiceApplyModeRemoteToLocalStartFailure(t *testing.T) {
	svc, m, _ := newTestService(t)
	remoteURL := "http://192.168.31.184:8080"
	if _, err := svc.ApplyMode(appsettings.ModeRemote, remoteURL); err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")

	// Hold the port the saved listen selection asks for.
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer hold.Close()
	takenPort := hold.Addr().(*net.TCPAddr).Port
	saved, found, err := appsettings.Load(savedPath)
	if err != nil || !found {
		t.Fatalf("persisted settings = found=%v err=%v", found, err)
	}
	saved.Port = takenPort
	if err := appsettings.Save(savedPath, saved); err != nil {
		t.Fatalf("persist occupied port: %v", err)
	}

	if _, err := svc.ApplyMode(appsettings.ModeLocal, ""); err == nil || !strings.Contains(err.Error(), "端口已被占用") {
		t.Fatalf("ApplyMode(local) with an occupied port = %v, want a port-in-use error", err)
	}
	if got := desktop.remoteHubURL(); got != remoteURL {
		t.Fatalf("runtime remote URL = %q after a failed start, want %q", got, remoteURL)
	}
	if got := desktop.inboxBaseURL(); got != remoteURL {
		t.Fatalf("inboxBaseURL = %q after a failed start, want the remote %q", got, remoteURL)
	}
	if got := m.URL(); got != "" {
		t.Fatalf("local Hub reported running at %q after a failed start", got)
	}
	after, found, err := appsettings.Load(savedPath)
	if err != nil || !found || after.Mode != appsettings.ModeRemote || after.Port != takenPort {
		t.Fatalf("failed switch changed the settings: %+v found=%v err=%v", after, found, err)
	}
	st, err := svc.GetState()
	if err != nil {
		t.Fatalf("GetState after a failed switch: %v", err)
	}
	if st.Mode != appsettings.ModeRemote || st.Running {
		t.Fatalf("state after a failed switch = %+v, want the unchanged remote mode", st)
	}
}

// TestServiceApplyModeIsRaceFreeWithConcurrentStateReads runs the switch while
// the settings window polls GetState and the tray/menu paths resolve the inbox
// URL, which is exactly where the runtime mode is read from several goroutines.
// Run with -race this pins the locking of desktop.remoteURL.
func TestServiceApplyModeIsRaceFreeWithConcurrentStateReads(t *testing.T) {
	svc, m, _ := newTestService(t)
	remoteURL := "http://192.168.31.184:8080"

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = svc.GetState()
				_ = desktop.inboxBaseURL()
			}
		}()
	}
	if _, err := svc.ApplyMode(appsettings.ModeRemote, remoteURL); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	if _, err := svc.ApplyMode(appsettings.ModeLocal, ""); err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("ApplyMode(local): %v", err)
	}
	close(stop)
	wg.Wait()
	if got := m.URL(); got == "" {
		t.Fatal("the Hub is not running after switching back to local mode")
	}
}

// TestServiceRemoteModeGuardsLocalHub proves that a settings window in remote
// mode never claims this machine's Hub can generate codes, list devices or
// rebind: the remote Hub owns all of that.
func TestServiceRemoteModeGuardsLocalHub(t *testing.T) {
	svc, _, _ := newTestService(t)

	st, err := svc.ApplyMode(appsettings.ModeRemote, "http://192.168.31.184:8080")
	if err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	if st.Running || st.URL != "" || len(st.URLs) != 0 {
		t.Fatalf("remote state = %+v, want no local Hub reported as running", st)
	}
	if st.BindAddress == "" || st.Port == 0 {
		t.Fatalf("remote state = %+v, want the saved bind address/port kept", st)
	}

	wantRemote := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s succeeded in remote mode", what)
		}
		if !strings.Contains(err.Error(), "远程 Hub 模式") {
			t.Fatalf("%s error = %v, want the remote-mode message", what, err)
		}
	}
	_, err = svc.CreatePairingCode()
	wantRemote("CreatePairingCode", err)
	_, err = svc.PairingCodeActive()
	wantRemote("PairingCodeActive", err)
	_, err = svc.ListPairedDevices()
	wantRemote("ListPairedDevices", err)
	wantRemote("RevokePairedDevice", svc.RevokePairedDevice("d1"))
	_, err = svc.ApplyNetwork("127.0.0.1", freePort(t))
	wantRemote("ApplyNetwork", err)
	_, err = svc.ApplyNetworkConfig("127.0.0.1", nil, freePort(t))
	wantRemote("ApplyNetworkConfig", err)
	_, err = svc.ApplyDualNetwork("192.168.1.10", "100.64.0.1", freePort(t))
	wantRemote("ApplyDualNetwork", err)

	// Switching back to local starts the Hub again instead of asking for a
	// restart; the remote address must be dropped.
	st, err = svc.ApplyMode(appsettings.ModeLocal, "")
	if err != nil {
		t.Fatalf("ApplyMode(local): %v", err)
	}
	if st.Mode != appsettings.ModeLocal || st.RemoteURL != "" {
		t.Fatalf("ApplyMode(local) state = %+v, want local mode", st)
	}
	if !st.Running {
		t.Fatalf("ApplyMode(local) state = %+v, want the local Hub running again", st)
	}
}

// TestServiceRemoteShellGetState covers the shipped remote-mode process: no
// local Hub is started at all, so the state must report remote mode and no
// running Hub.
func TestServiceRemoteShellGetState(t *testing.T) {
	svc, _, _ := newTestService(t)
	remoteURL := "http://192.168.31.184:8080"
	// This is what the hot switch leaves behind in remote mode: no local Hub and
	// the runtime remote URL set.
	if _, err := svc.ApplyMode(appsettings.ModeRemote, remoteURL); err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	if got := desktop.remoteHubURL(); got != remoteURL {
		t.Fatalf("runtime remote URL = %q, want %q", got, remoteURL)
	}

	st, err := svc.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.Mode != appsettings.ModeRemote || st.RemoteURL != remoteURL {
		t.Fatalf("GetState = %+v, want remote mode at %s", st, remoteURL)
	}
	if st.Running || st.URL != "" {
		t.Fatalf("GetState = %+v, want running=false and no URL", st)
	}
	if st.URLs != nil {
		t.Fatalf("GetState URLs = %#v, want nil in remote mode", st.URLs)
	}
	if st.BindAddress == "" || st.Port == 0 {
		t.Fatalf("GetState = %+v, want the saved bind address/port returned", st)
	}
	if _, err := svc.ListPairedDevices(); err == nil || !strings.Contains(err.Error(), "远程 Hub 模式") {
		t.Fatalf("ListPairedDevices = %v, want the remote-mode message", err)
	}
}

// TestDesktopSettingsFileIsPrivateAndHasNoSession pins the two properties the
// desktop.json write must keep once the device token lives in it: the file is
// readable by this user only (0600) and it never carries a browser session
// cookie or a password-like field, only the device token it may replay.
func TestDesktopSettingsFileIsPrivateAndHasNoSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.json")
	token := testDeviceToken // 43 base64url chars, the shape the Hub issues
	st := appsettings.Settings{
		Mode:               appsettings.ModeLocal,
		BindAddress:        "192.168.1.5",
		Port:               8080,
		DesktopDeviceID:    "dev_local",
		DesktopDeviceToken: token,
	}
	if err := appsettings.Save(path, st); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("desktop.json permissions = %04o, want 0600", perm)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range []string{"flowdrop_session", "password", "secret", "csrf"} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("desktop.json contains %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(string(raw), token) {
		t.Fatal("desktop.json is missing the device token it is supposed to keep")
	}
}
