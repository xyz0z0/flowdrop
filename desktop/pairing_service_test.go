package main

// PA-06: the settings-window pairing bridge, exercised against a real
// in-process Hub on a temporary data directory through the bound
// SettingsService. No Wails application and no GUI is involved: this is the
// same state the service is in before the windows exist.
//
// Flow: local code -> HTTP pair -> local device list -> local revoke -> the
// paired token is dead over HTTP -> every bridge method reports a stopped Hub
// as unusable instead of pretending.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/pkg/hub"
)

func pairingHTTP(t *testing.T, method, url, body string, mutate ...func(*http.Request)) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, m := range mutate {
		m(req)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return res.StatusCode, b
}

func bearerSessionAuthenticated(t *testing.T, base, token string) bool {
	t.Helper()
	status, body := pairingHTTP(t, http.MethodGet, base+"/api/v1/auth/session", "",
		func(r *http.Request) {
			r.Header.Set("Origin", base)
			r.Header.Set("Authorization", "Bearer "+token)
		})
	if status != http.StatusOK {
		t.Fatalf("bearer session = %d body=%s", status, body)
	}
	return strings.Contains(string(body), `"authenticated":true`)
}

func TestSettingsPairingBridgeEndToEnd(t *testing.T) {
	svc, m, _ := newTestService(t)

	// A fresh temporary Hub has no paired device yet.
	devices, err := svc.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices on fresh Hub: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("fresh Hub devices = %+v, want none", devices)
	}

	code, err := svc.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	// The code crosses the bridge as a string so a leading zero survives.
	if !regexp.MustCompile(`^[0-9]{4}$`).MatchString(code.Code) {
		t.Fatalf("code %q is not 4 decimal digits", code.Code)
	}
	if d := time.Until(time.UnixMilli(code.ExpiresAt)); d < 60*time.Second || d > 130*time.Second {
		t.Fatalf("code expiry %v is not ~120s away", d)
	}
	// The settings page polls this to know whether the code it displays is
	// still usable; right after creation it must say yes.
	if active, err := svc.PairingCodeActive(); err != nil || !active {
		t.Fatalf("PairingCodeActive after create = %v, %v; want true, nil", active, err)
	}

	// A phone pairs with that code over the frozen HTTP route.
	base := m.URL()
	status, body := pairingHTTP(t, http.MethodPost, base+"/api/v1/auth/pair",
		fmt.Sprintf(`{"code":%q,"device_name":"pixel-test","native":true}`, code.Code),
		func(r *http.Request) { r.Header.Set("Origin", base) })
	if status != http.StatusOK {
		t.Fatalf("pair = %d body=%s", status, body)
	}
	var paired struct {
		DeviceID    string `json:"device_id"`
		DeviceToken string `json:"device_token"`
	}
	if err := json.Unmarshal(body, &paired); err != nil {
		t.Fatalf("decode pair response: %v body=%s", err, body)
	}
	if paired.DeviceID == "" || paired.DeviceToken == "" {
		t.Fatalf("pair response missing device id/token: %s", body)
	}
	// The code is consumed now, so the local bridge reports it inactive well
	// before its 120s deadline: that is what hides the code in the UI.
	if active, err := svc.PairingCodeActive(); err != nil || active {
		t.Fatalf("PairingCodeActive after pair = %v, %v; want false, nil", active, err)
	}

	// The settings list shows the device with its name and both timestamps.
	devices, err = svc.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices after pair: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices = %+v, want exactly one", devices)
	}
	got := devices[0]
	if got.ID != paired.DeviceID || got.Name != "pixel-test" {
		t.Fatalf("device = %+v, want %s / pixel-test", got, paired.DeviceID)
	}
	if got.CreatedAt == 0 || got.LastSeenAt == 0 {
		t.Fatalf("device timestamps not set: %+v", got)
	}
	if !bearerSessionAuthenticated(t, base, paired.DeviceToken) {
		t.Fatal("paired device token does not authenticate before revocation")
	}

	// Revoking from the settings window drops the device and its HTTP session.
	if err := svc.RevokePairedDevice(got.ID); err != nil {
		t.Fatalf("RevokePairedDevice: %v", err)
	}
	devices, err = svc.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices after revoke: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices after revoke = %+v, want none", devices)
	}
	if bearerSessionAuthenticated(t, base, paired.DeviceToken) {
		t.Fatal("revoked device token still authenticates over HTTP")
	}
	if err := svc.RevokePairedDevice("does-not-exist"); err == nil {
		t.Fatal("revoking an unknown device id succeeded")
	}
	if err := svc.RevokePairedDevice("   "); err == nil {
		t.Fatal("revoking an empty device id succeeded")
	}

	// Once the Hub is stopped, pairing is reported as unavailable with the
	// message the settings window shows, not as an empty successful list.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Stop(ctx); err != nil {
		t.Fatalf("stop test hub: %v", err)
	}
	if _, err := svc.CreatePairingCode(); err == nil || !strings.Contains(err.Error(), "Hub 未运行") {
		t.Fatalf("CreatePairingCode with stopped Hub = %v, want the Hub-not-running message", err)
	}
	if _, err := svc.PairingCodeActive(); err == nil || !strings.Contains(err.Error(), "Hub 未运行") {
		t.Fatalf("PairingCodeActive with stopped Hub = %v, want the Hub-not-running message", err)
	}
	if _, err := svc.ListPairedDevices(); err == nil || !strings.Contains(err.Error(), "Hub 未运行") {
		t.Fatalf("ListPairedDevices with stopped Hub = %v, want the Hub-not-running message", err)
	}
	if err := svc.RevokePairedDevice("any"); err == nil || !strings.Contains(err.Error(), "Hub 未运行") {
		t.Fatalf("RevokePairedDevice with stopped Hub = %v, want the Hub-not-running message", err)
	}
}

// TestPairingDTOKeepsCodeAsString pins the bridge JSON contract the settings
// page relies on: a four-digit code stays a string (leading zero included) and
// the device fields keep their frozen snake_case names.
func TestPairingDTOKeepsCodeAsString(t *testing.T) {
	raw, err := json.Marshal(PairingCode{Code: "0073", ExpiresAt: 1700000000000})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"code":"0073","expires_at":1700000000000}` {
		t.Fatalf("PairingCode JSON = %s", raw)
	}
	raw, err = json.Marshal(PairedDevice{ID: "d1", Name: "手机", CreatedAt: 11, LastSeenAt: 22})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"id":"d1"`, `"name":"手机"`, `"created_at":11`, `"last_seen_at":22`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("PairedDevice JSON %s is missing %s", raw, want)
		}
	}
}

// TestSettingsAssetsPairDevicesInsteadOfPasswords checks that the local
// settings asset asks for a pairing code and no longer carries the admin
// password panel, and that device names are inserted as text, not HTML.
func TestSettingsAssetsPairDevicesInsteadOfPasswords(t *testing.T) {
	html, err := os.ReadFile(filepath.Join("assets", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	for _, want := range []string{`id="pair-add"`, `id="pairing-code-value"`, `id="pairing-status"`, `id="device-list"`} {
		if !strings.Contains(page, want) {
			t.Errorf("settings page is missing %s", want)
		}
	}
	for _, unwanted := range []string{`type="password"`, `id="set-admin"`, `id="admin-card"`} {
		if strings.Contains(page, unwanted) {
			t.Errorf("settings page still contains the admin password panel (%s)", unwanted)
		}
	}

	js, err := os.ReadFile(filepath.Join("assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(js)
	if !strings.Contains(script, `name.textContent = dev.name`) {
		t.Error("device name is not inserted with textContent")
	}
	for _, forbidden := range []string{"InitializeAdmin", "innerHTML = dev", "innerHTML = name"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("settings script still uses %q", forbidden)
		}
	}
	if !strings.Contains(script, "clearPairingCode") {
		t.Error("settings script has no code-clearing path")
	}
	// A consumed code must be hidden by the authoritative local answer, with a
	// success banner, not by guessing from the countdown or the device list.
	for _, want := range []string{"PairingCodeActive", "pairing-status", "配对成功，配对码已使用并自动隐藏。"} {
		if !strings.Contains(script, want) {
			t.Errorf("settings script is missing %q", want)
		}
	}
}

// TestVisibleDevicesHidesLocalWindow pins the filtering rule: the window's own
// device record never appears in the revocable list (it is shown as a separate
// informational row instead).
func TestVisibleDevicesHidesLocalWindow(t *testing.T) {
	devices := []hub.PairedDevice{
		{ID: "dev_local", Name: "本机桌面 · host", CreatedAt: 1, LastSeenAt: 2},
		{ID: "dev_phone", Name: "手机", CreatedAt: 3, LastSeenAt: 4},
	}
	got := visibleDevices(devices, "dev_local")
	if len(got) != 1 || got[0].ID != "dev_phone" || got[0].Name != "手机" {
		t.Fatalf("visibleDevices = %+v, want only the phone", got)
	}
	if len(visibleDevices(devices, "")) != 2 {
		t.Fatal("visibleDevices without a saved local id dropped a device")
	}
	if len(visibleDevices(devices, "dev_unknown")) != 2 {
		t.Fatal("visibleDevices dropped a device that is not the local window")
	}
	if got := visibleDevices(nil, "dev_local"); got == nil {
		t.Fatal("visibleDevices returned nil; the settings page expects an array")
	}
}

// TestListPairedDevicesHidesLocalWindow runs the same rule through the real
// bridge: two devices exist on the Hub, the desktop.json identifies one of them
// as this window, and only the other one is listable/revocable.
func TestListPairedDevicesHidesLocalWindow(t *testing.T) {
	svc, m, _ := newTestService(t)
	pair := func(name string) (string, string) {
		code, err := m.CreatePairingCode()
		if err != nil {
			t.Fatalf("CreatePairingCode: %v", err)
		}
		deviceID, token, err := pairDesktopDevice(&http.Client{Timeout: 10 * time.Second}, m.URL(), name, code.Code)
		if err != nil {
			t.Fatalf("pair %q: %v", name, err)
		}
		return deviceID, token
	}
	localID, localToken := pair(desktopDeviceName())
	pair("手机")
	if err := appsettings.Save(desktop.settingsPath, appsettings.Settings{
		Mode:               appsettings.ModeLocal,
		BindAddress:        "127.0.0.1",
		Port:               8080,
		DesktopDeviceID:    localID,
		DesktopDeviceToken: localToken,
	}); err != nil {
		t.Fatal(err)
	}
	devices, err := svc.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].Name != "手机" {
		t.Fatalf("ListPairedDevices = %+v, want only the phone (the local window is not revocable)", devices)
	}
}

// TestGetStateLocalDeviceName covers the informational row: it is set only in
// local mode and only once a device token is saved.
func TestGetStateLocalDeviceName(t *testing.T) {
	svc, _, _ := newTestService(t)
	st, err := svc.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.LocalDeviceName != "" {
		t.Fatalf("local_device_name = %q without a saved token, want empty", st.LocalDeviceName)
	}
	if err := appsettings.Save(desktop.settingsPath, appsettings.Settings{
		Mode:               appsettings.ModeLocal,
		BindAddress:        "127.0.0.1",
		Port:               8080,
		DesktopDeviceID:    "dev_local",
		DesktopDeviceToken: testDeviceToken,
	}); err != nil {
		t.Fatal(err)
	}
	st, err = svc.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.LocalDeviceName != desktopDeviceName() {
		t.Fatalf("local_device_name = %q, want %q", st.LocalDeviceName, desktopDeviceName())
	}
	// Remote mode: the token belongs to no Hub this process owns, so the row
	// must stay empty even though one is stored.
	if err := appsettings.Save(desktop.settingsPath, appsettings.Settings{
		Mode:               appsettings.ModeRemote,
		RemoteURL:          "http://192.168.1.9:8080",
		BindAddress:        "127.0.0.1",
		Port:               8080,
		DesktopDeviceID:    "dev_local",
		DesktopDeviceToken: testDeviceToken,
	}); err != nil {
		t.Fatal(err)
	}
	st, err = svc.GetState()
	if err != nil {
		t.Fatalf("GetState in remote mode: %v", err)
	}
	if st.Mode != appsettings.ModeRemote {
		t.Fatalf("mode = %q, want remote", st.Mode)
	}
	if st.LocalDeviceName != "" {
		t.Fatalf("local_device_name = %q in remote mode, want empty", st.LocalDeviceName)
	}
}
