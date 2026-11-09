package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/desktop/internal/hubmgr"
)

const testDeviceToken = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFG" // 43 chars, base64url raw

// roundTripRecorder fails the test if it is ever asked to perform a request.
type roundTripRecorder struct {
	t      *testing.T
	called bool
}

func (r *roundTripRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	r.called = true
	return nil, io.ErrUnexpectedEOF
}

func TestPairDesktopDeviceFirstPairing(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/auth/pair" {
			t.Errorf("path = %s, want /api/v1/auth/pair", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		// The desktop is not a browser page: it must not claim an Origin, so the
		// Hub's originAllowed treats it as a non-browser client.
		if origin := r.Header.Get("Origin"); origin != "" {
			t.Errorf("unexpected Origin header %q", origin)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"authenticated":true,"device_id":"dev_ABC-123","device_token":"`+testDeviceToken+`"}`)
	}))
	defer srv.Close()

	client := &http.Client{}
	deviceID, token, err := pairDesktopDevice(client, srv.URL+"/", "本机桌面 · testhost", "0427")
	if err != nil {
		t.Fatalf("pairDesktopDevice: %v", err)
	}
	if deviceID != "dev_ABC-123" {
		t.Fatalf("deviceID = %q, want dev_ABC-123", deviceID)
	}
	if token != testDeviceToken {
		t.Fatalf("token = %q, want %q", token, testDeviceToken)
	}
	if native, _ := got["native"].(bool); !native {
		t.Fatalf("native = %v, want true (body %v)", got["native"], got)
	}
	if name, _ := got["device_name"].(string); name != "本机桌面 · testhost" {
		t.Fatalf("device_name = %q, want it sent unchanged", name)
	}
	if code, _ := got["code"].(string); code != "0427" {
		t.Fatalf("code = %q, want the code sent unchanged", code)
	}
}

func TestPairDesktopDeviceInvalidCode(t *testing.T) {
	const code = "9137"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":"pairing_invalid","message":"pairing code is not valid"}}`)
	}))
	defer srv.Close()

	deviceID, token, err := pairDesktopDevice(&http.Client{}, srv.URL, "本机桌面 · testhost", code)
	if err == nil {
		t.Fatal("pairDesktopDevice accepted a 401 response")
	}
	if deviceID != "" || token != "" {
		t.Fatalf("deviceID/token = %q/%q, want empty on error", deviceID, token)
	}
	if strings.Contains(err.Error(), code) {
		t.Fatalf("error %q leaks the pairing code", err)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "pairing_invalid") {
		t.Fatalf("error %q should carry the status and the hub error code", err)
	}
}

func TestPairDesktopDeviceMissingToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"authenticated":true,"device_id":"dev_1"}`)
	}))
	defer srv.Close()

	deviceID, token, err := pairDesktopDevice(&http.Client{}, srv.URL, "本机桌面 · testhost", "1234")
	if err == nil {
		t.Fatal("pairDesktopDevice accepted a response without device_token")
	}
	if deviceID != "" || token != "" {
		t.Fatalf("deviceID/token = %q/%q, want empty on error", deviceID, token)
	}
}

func TestPairDesktopDeviceMissingDeviceID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"authenticated":true,"device_token":"`+testDeviceToken+`"}`)
	}))
	defer srv.Close()

	deviceID, token, err := pairDesktopDevice(&http.Client{}, srv.URL, "本机桌面 · testhost", "1234")
	if err == nil {
		t.Fatal("pairDesktopDevice accepted a response without device_id")
	}
	if deviceID != "" || token != "" {
		t.Fatalf("deviceID/token = %q/%q, want empty on error", deviceID, token)
	}
	if strings.Contains(err.Error(), testDeviceToken) {
		t.Fatalf("error %q leaks the device token", err)
	}
}

func TestPairDesktopDeviceRejectsBadBaseURL(t *testing.T) {
	for _, baseURL := range []string{"", "   ", "ftp://x", "file:///tmp", "http://", "192.168.1.5:8080"} {
		rec := &roundTripRecorder{t: t}
		client := &http.Client{Transport: rec}
		deviceID, token, err := pairDesktopDevice(client, baseURL, "本机桌面 · testhost", "4321")
		if err == nil {
			t.Fatalf("pairDesktopDevice(%q) = %q/%q, want an error", baseURL, deviceID, token)
		}
		if rec.called {
			t.Fatalf("pairDesktopDevice(%q) sent a request despite an invalid base URL", baseURL)
		}
		if strings.Contains(err.Error(), "4321") {
			t.Fatalf("error %q leaks the pairing code", err)
		}
	}
}

func TestPairDesktopDeviceRejectsNilClient(t *testing.T) {
	if _, _, err := pairDesktopDevice(nil, "http://127.0.0.1:8080", "本机桌面", "1234"); err == nil {
		t.Fatal("pairDesktopDevice(nil client) = nil error")
	}
}

func TestDesktopBootstrapJSEmptyToken(t *testing.T) {
	if js := desktopBootstrapJS(""); js != "" {
		t.Fatalf("desktopBootstrapJS(\"\") = %q, want empty", js)
	}
}

func TestDesktopBootstrapJSUsesJSONLiteral(t *testing.T) {
	js := desktopBootstrapJS(testDeviceToken)
	if js == "" {
		t.Fatal("desktopBootstrapJS returned an empty script for a non-empty token")
	}
	literal, err := json.Marshal(testDeviceToken)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, "var token = "+string(literal)+";") {
		t.Fatalf("script does not embed the token as a JSON literal:\n%s", js)
	}
	// Exactly the two frozen requests, both against the session endpoint.
	if got := strings.Count(js, "fetch("); got != 2 {
		t.Fatalf("script contains %d fetch calls, want exactly 2 (no other network requests):\n%s", got, js)
	}
	if got := strings.Count(js, sessionPath); got != 1 {
		t.Fatalf("script references %s %d times, want 1 (the URL is shared):\n%s", sessionPath, got, js)
	}
	for _, want := range []string{
		"window.__flowdropDesktopBootstrap",
		`credentials: "same-origin"`,
		`Authorization: "Bearer " + token`,
		"location.reload()",
		".catch(function () {})",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("script is missing %q:\n%s", want, js)
		}
	}
	for _, forbidden := range []string{"alert(", "console.", "setInterval(", "setTimeout(", "localStorage"} {
		if strings.Contains(js, forbidden) {
			t.Fatalf("script must stay silent, found %q:\n%s", forbidden, js)
		}
	}
}

func TestDesktopBootstrapJSInsertsGuardedTokenLiteral(t *testing.T) {
	// Quotes, backslashes and a closing script tag must all survive as a single
	// JSON string literal instead of breaking the injected <script>.
	const hostile = `ab"cd\ef</script><script>alert(1)</script>`
	js := desktopBootstrapJS(hostile)
	literal, err := json.Marshal(hostile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(js, "var token = "+string(literal)+";") {
		t.Fatalf("script does not embed the escaped literal %s:\n%s", literal, js)
	}
	if strings.Contains(js, "</script>") {
		t.Fatalf("script contains a raw closing script tag:\n%s", js)
	}
	if strings.Contains(js, `"ab"cd`) {
		t.Fatalf("script contains an unescaped quote from the token:\n%s", js)
	}
	// Go's JSON encoder writes "<" as \u003c, so the literal keeps one opening
	// and one closing quote and no raw newline.
	if got := strings.Count(strings.SplitN(js, "\n", 4)[3], `var token = "`); got != 1 {
		t.Fatalf("token literal opening quote count = %d, want 1:\n%s", got, js)
	}
}

func TestDesktopDeviceNameIsStableAndNonEmpty(t *testing.T) {
	first := desktopDeviceName()
	second := desktopDeviceName()
	if first == "" {
		t.Fatal("desktopDeviceName() is empty")
	}
	if first != second {
		t.Fatalf("desktopDeviceName() is unstable: %q != %q", first, second)
	}
	if !strings.Contains(first, "本机桌面") {
		t.Fatalf("desktopDeviceName() = %q, want it to contain 本机桌面", first)
	}
}

// pairResponseServer answers every request with a successful native pairing and
// counts how many requests actually arrived.
func pairResponseServer(t *testing.T, count *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*count++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"authenticated":true,"device_id":"dev_local_1","device_token":"`+testDeviceToken+`"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// sameAppSettings compares two settings values including the device identity
// (Settings holds a slice, so it is not comparable with ==).
func sameAppSettings(a, b appsettings.Settings) bool {
	return a.Mode == b.Mode && a.RemoteURL == b.RemoteURL &&
		a.BindAddress == b.BindAddress && a.Port == b.Port &&
		slices.Equal(a.AdditionalBindAddresses, b.AdditionalBindAddresses) &&
		a.DesktopDeviceID == b.DesktopDeviceID && a.DesktopDeviceToken == b.DesktopDeviceToken
}

func TestPairLocalHubDeviceSkipsRequestWithSavedToken(t *testing.T) {
	calls := 0
	srv := pairResponseServer(t, &calls)
	rec := &roundTripRecorder{t: t}
	st := appsettings.Settings{
		BindAddress:        "127.0.0.1",
		Port:               8080,
		DesktopDeviceID:    "dev_old",
		DesktopDeviceToken: testDeviceToken,
	}
	got, err := pairLocalHubDevice(&http.Client{Transport: rec}, srv.URL, desktopDeviceName(), st, func() (string, error) {
		t.Error("newCode was called although a device token is already saved")
		return "", nil
	})
	if err != nil {
		t.Fatalf("pairLocalHubDevice with a saved token: %v", err)
	}
	if rec.called || calls != 0 {
		t.Fatal("pairLocalHubDevice sent a request although a device token is already saved")
	}
	if !sameAppSettings(got, st) {
		t.Fatalf("settings = %+v, want them returned unchanged (%+v)", got, st)
	}
}

func TestPairLocalHubDeviceFillsIdentity(t *testing.T) {
	var sentCode string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		sentCode = body.Code
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"authenticated":true,"device_id":"dev_local_1","device_token":"`+testDeviceToken+`"}`)
	}))
	defer srv.Close()
	st := appsettings.Settings{BindAddress: "127.0.0.1", Port: 8080}
	got, err := pairLocalHubDevice(&http.Client{}, srv.URL, "本机桌面 · testhost", st, func() (string, error) {
		return "0427", nil
	})
	if err != nil {
		t.Fatalf("pairLocalHubDevice: %v", err)
	}
	if got.DesktopDeviceID != "dev_local_1" || got.DesktopDeviceToken != testDeviceToken {
		t.Fatalf("settings = %+v, want the paired device id and token filled in", got)
	}
	// Everything else is preserved: pairing only learns the identity.
	if got.BindAddress != st.BindAddress || got.Port != st.Port {
		t.Fatalf("settings = %+v, want bind/port untouched", got)
	}
	if sentCode != "0427" {
		t.Fatalf("pairing code sent = %q, want the one from newCode", sentCode)
	}
}

func TestPairLocalHubDeviceNewCodeFailureKeepsSettings(t *testing.T) {
	calls := 0
	srv := pairResponseServer(t, &calls)
	rec := &roundTripRecorder{t: t}
	st := appsettings.Settings{BindAddress: "127.0.0.1", Port: 8080}
	got, err := pairLocalHubDevice(&http.Client{Transport: rec}, srv.URL, "本机桌面 · testhost", st, func() (string, error) {
		return "", io.ErrUnexpectedEOF
	})
	if err == nil {
		t.Fatal("pairLocalHubDevice accepted a failing newCode")
	}
	if !sameAppSettings(got, st) {
		t.Fatalf("settings = %+v, want them unchanged (%+v)", got, st)
	}
	if rec.called || calls != 0 {
		t.Fatal("pairLocalHubDevice sent a pairing request although no code was produced")
	}
}

func TestPairLocalHubDevicePairFailureKeepsSettings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"code":"pairing_invalid","message":"pairing code is not valid"}}`)
	}))
	defer srv.Close()
	st := appsettings.Settings{BindAddress: "127.0.0.1", Port: 8080}
	got, err := pairLocalHubDevice(&http.Client{}, srv.URL, "本机桌面 · testhost", st, func() (string, error) {
		return "9137", nil
	})
	if err == nil {
		t.Fatal("pairLocalHubDevice accepted a 401 response")
	}
	if !sameAppSettings(got, st) {
		t.Fatalf("settings = %+v, want them unchanged (%+v)", got, st)
	}
	if strings.Contains(err.Error(), "9137") || strings.Contains(err.Error(), testDeviceToken) {
		t.Fatalf("error %q leaks the pairing code or a token", err)
	}
}

// newLoginTestState builds the shell-side state ensureDesktopLogin needs against
// a real in-process Hub, without a Wails application: the startup path calls it
// before any window exists.
func newLoginTestState(t *testing.T) (*desktopState, *hubmgr.Manager, string) {
	t.Helper()
	_, m, _ := newTestService(t)
	d := &desktopState{
		manager:      m,
		settingsPath: desktop.settingsPath,
		loginClient:  &http.Client{Timeout: desktopLoginTimeout},
	}
	return d, m, desktop.settingsPath
}

func TestEnsureDesktopLoginPairsAndPersists(t *testing.T) {
	d, m, path := newLoginTestState(t)
	st := appsettings.Settings{BindAddress: "127.0.0.1", Port: 8080}

	next := d.ensureDesktopLogin(st)
	// The token must never be printed by this helper; only the shape of what
	// came back is asserted.
	if next.DesktopDeviceToken == "" {
		t.Fatal("ensureDesktopLogin did not pair this window with the local Hub")
	}
	if next.DesktopDeviceID == "" {
		t.Fatal("ensureDesktopLogin returned no device id")
	}
	if got := d.desktopDeviceToken(); got != next.DesktopDeviceToken {
		t.Fatal("the paired token was not published for the inbox window")
	}
	// A pairing really happened on the Hub side.
	if devices, err := m.ListPairedDevices(); err != nil || len(devices) != 1 {
		t.Fatalf("paired devices = %d, %v; want exactly the local window", len(devices), err)
	}
	// And the token was written next to the rest of the settings, 0600.
	saved, found, err := appsettings.Load(path)
	if err != nil || !found {
		t.Fatalf("Load after pairing = found=%v err=%v", found, err)
	}
	if saved.DesktopDeviceToken != next.DesktopDeviceToken || saved.DesktopDeviceID != next.DesktopDeviceID {
		t.Fatal("the paired identity was not saved to desktop.json")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("desktop.json permissions = %04o, want 0600", perm)
	}
}

func TestEnsureDesktopLoginReusesSavedIdentity(t *testing.T) {
	d, m, _ := newLoginTestState(t)
	first := d.ensureDesktopLogin(appsettings.Settings{BindAddress: "127.0.0.1", Port: 8080})
	if first.DesktopDeviceToken == "" {
		t.Fatal("first ensureDesktopLogin did not pair")
	}
	// A restart loads that file: no new pairing request, no second device row.
	second := d.ensureDesktopLogin(first)
	if second.DesktopDeviceID != first.DesktopDeviceID || second.DesktopDeviceToken != first.DesktopDeviceToken {
		t.Fatal("ensureDesktopLogin changed an already saved identity")
	}
	if devices, err := m.ListPairedDevices(); err != nil || len(devices) != 1 {
		t.Fatalf("paired devices = %d, %v; want still exactly one", len(devices), err)
	}
}

func TestEnsureDesktopLoginRevokesReplacedDevice(t *testing.T) {
	d, m, _ := newLoginTestState(t)
	first := d.ensureDesktopLogin(appsettings.Settings{BindAddress: "127.0.0.1", Port: 8080})
	if first.DesktopDeviceID == "" {
		t.Fatal("first ensureDesktopLogin did not pair")
	}
	// A file whose token no longer passes Normalize keeps the old device id but
	// no token, so the shell pairs again and must drop the stale record.
	stale := appsettings.Settings{
		BindAddress:     "127.0.0.1",
		Port:            8080,
		DesktopDeviceID: first.DesktopDeviceID,
	}
	second := d.ensureDesktopLogin(stale)
	if second.DesktopDeviceID == "" || second.DesktopDeviceID == first.DesktopDeviceID {
		t.Fatalf("device id after re-pairing = %q, want a new one", second.DesktopDeviceID)
	}
	devices, err := m.ListPairedDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 || devices[0].ID != second.DesktopDeviceID {
		t.Fatalf("paired devices = %+v, want only the new identity", devices)
	}
}

func TestEnsureDesktopLoginLeavesRemoteModeAlone(t *testing.T) {
	d, m, _ := newLoginTestState(t)
	st := appsettings.Settings{Mode: appsettings.ModeRemote, RemoteURL: "http://192.168.1.9:8080"}
	got := d.ensureDesktopLogin(st)
	if !sameAppSettings(got, st) {
		t.Fatalf("remote settings = %+v, want them unchanged (%+v)", got, st)
	}
	if got := d.desktopDeviceToken(); got != "" {
		t.Fatal("remote mode published a local device token")
	}
	if devices, err := m.ListPairedDevices(); err != nil || len(devices) != 0 {
		t.Fatalf("remote mode paired a device: %+v, %v", devices, err)
	}
}

func TestDesktopInboxBootstrapJSOnlyWithToken(t *testing.T) {
	if js := desktopInboxBootstrapJS(""); js != "" {
		t.Fatalf("desktopInboxBootstrapJS(\"\") = %q, want nothing injected", js)
	}
	js := desktopInboxBootstrapJS(testDeviceToken)
	if !strings.HasPrefix(js, "\n") {
		t.Fatalf("injected script is not separated from the previous one by a newline: %q", js)
	}
	if strings.TrimPrefix(js, "\n") != desktopBootstrapJS(testDeviceToken) {
		t.Fatal("the injected script is not exactly desktopBootstrapJS(token)")
	}
}

// TestDesktopBootstrapJSBoundsReloadLoop pins the loop guard: the script may
// only reload while a sessionStorage budget is left, and a page load that is
// already authenticated resets that budget.
func TestDesktopBootstrapJSBoundsReloadLoop(t *testing.T) {
	if js := desktopBootstrapJS(""); js != "" {
		t.Fatalf("desktopBootstrapJS(\"\") = %q, want empty (no token, no script)", js)
	}
	js := desktopBootstrapJS(testDeviceToken)
	for _, want := range []string{
		"var reloadKey = \"flowdropBootstrapReloads\";",
		"var maxReloads = 2;",
		"window.sessionStorage.getItem(reloadKey)",
		"window.sessionStorage.setItem(reloadKey, String(used + 1))",
		"window.sessionStorage.removeItem(reloadKey)",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("script is missing %q:\n%s", want, js)
		}
	}
	// Exactly one reload, and it is behind the budget check.
	if got := strings.Count(js, "location.reload()"); got != 1 {
		t.Fatalf("script calls location.reload() %d times, want 1:\n%s", got, js)
	}
	capAt := strings.Index(js, "used < 0 || used >= maxReloads")
	reloadAt := strings.Index(js, "location.reload()")
	if capAt < 0 || reloadAt < 0 || capAt > reloadAt {
		t.Fatalf("the reload is not guarded by the maxReloads check:\n%s", js)
	}
	// A throwing sessionStorage (privacy mode) must mean "never reload".
	if !strings.Contains(js, "return -1;") {
		t.Fatalf("script has no unreachable-storage path:\n%s", js)
	}
	// The reload only ever follows the Bearer path; the cookie path resets the
	// budget instead of reloading.
	if !strings.Contains(js, "if (renewed && renewed.authenticated === true) { reloadOnce(); }") {
		t.Fatalf("the reload is not triggered from the Bearer branch:\n%s", js)
	}
	if got := strings.Count(js, "clearReloads();"); got != 1 {
		t.Fatalf("script resets the reload budget %d times, want exactly 1 (the already-authenticated path):\n%s", got, js)
	}
	clearAt := strings.Index(js, "clearReloads();")
	bearerAt := strings.Index(js, `Authorization: "Bearer " + token`)
	if clearAt < 0 || bearerAt < 0 || clearAt > bearerAt {
		t.Fatalf("the budget reset is not on the cookie path before the Bearer request:\n%s", js)
	}
	// Same contract as before: two requests, one session URL, no timers.
	if got := strings.Count(js, "fetch("); got != 2 {
		t.Fatalf("script contains %d fetch calls, want exactly 2:\n%s", got, js)
	}
	if got := strings.Count(js, sessionPath); got != 1 {
		t.Fatalf("script references %s %d times, want 1:\n%s", sessionPath, got, js)
	}
}
