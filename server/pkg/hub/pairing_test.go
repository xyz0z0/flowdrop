package hub

// PA-04H coverage: the local pairing bridge (code, list, revoke), the metadata
// callback fed from actually bound listeners, the stable hub ID across a restart
// and the refusal paths before Start / after Stop.
//
// Hermetic: every Hub gets its own temp data dir and two ephemeral loopback
// listeners. No password is initialized anywhere, which is the point: device
// pairing must work on a fresh data directory.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"flowdrop/internal/api"
)

func pairingOptions(t *testing.T, dir string) Options {
	t.Helper()
	no := false
	return Options{
		ConfigPath:       filepath.Join(dir, "absent-config.json"),
		Listen:           "127.0.0.1:0",
		AdditionalListen: []string{"127.0.0.2:0"},
		DataDir:          filepath.Join(dir, "data"),
		CookieSecure:     &no,
		DiscoveryEnabled: &no,
	}
}

func pairingNew(t *testing.T, opts Options) *Hub {
	t.Helper()
	h, err := New(opts)
	if err != nil {
		t.Fatalf("hub.New: %v", err)
	}
	return h
}

func pairingStart(t *testing.T, h *Hub) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := h.Start(ctx); err != nil {
		t.Fatalf("hub.Start: %v", err)
	}
}

func pairingStop(t *testing.T, h *Hub) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := h.Stop(ctx); err != nil {
		t.Fatalf("hub.Stop: %v", err)
	}
}

func pairingDo(t *testing.T, method, url, body string, mutate ...func(*http.Request)) (int, []byte) {
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
	res, err := http.DefaultClient.Do(req)
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

// TestPairingLocalCodeListRevokeOnFreshDataDir walks the frozen local flow: a
// brand-new data directory (no admin password) can create a code, a client pairs
// with it over HTTP, the device shows up in the local list, and revoking it
// invalidates the device token again.
func TestPairingLocalCodeListRevokeOnFreshDataDir(t *testing.T) {
	dir := t.TempDir()
	h := pairingNew(t, pairingOptions(t, dir))
	pairingStart(t, h)
	defer pairingStop(t, h)

	code, err := h.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	if !regexp.MustCompile(`^[0-9]{4}$`).MatchString(code.Code) {
		t.Fatalf("code %q is not 4 decimal digits", code.Code)
	}
	if d := time.Until(time.UnixMilli(code.ExpiresAt)); d < 60*time.Second || d > 130*time.Second {
		t.Fatalf("code expiry %v is not ~120s away", d)
	}

	base := h.URL()
	status, body := pairingDo(t, http.MethodPost, base+"/api/v1/auth/pair",
		fmt.Sprintf(`{"code":%q,"device_name":"desktop-test","native":true}`, code.Code),
		func(r *http.Request) { r.Header.Set("Origin", base) })
	if status != http.StatusOK {
		t.Fatalf("pair = %d body=%s", status, body)
	}
	var pairRes struct {
		DeviceID    string `json:"device_id"`
		DeviceToken string `json:"device_token"`
		Hub         struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Endpoints []struct {
				URL  string `json:"url"`
				Kind string `json:"kind"`
			} `json:"endpoints"`
		} `json:"hub"`
	}
	if err := json.Unmarshal(body, &pairRes); err != nil {
		t.Fatalf("decode pair: %v body=%s", err, body)
	}
	if pairRes.DeviceID == "" || pairRes.DeviceToken == "" {
		t.Fatalf("pair response missing device id/token: %s", body)
	}
	if pairRes.Hub.ID == "" || pairRes.Hub.ID != h.hubInfo().ID {
		t.Fatalf("pair hub id %q != local hub id %q", pairRes.Hub.ID, h.hubInfo().ID)
	}
	local := h.hubInfo()
	if len(pairRes.Hub.Endpoints) != len(local.Endpoints) {
		t.Fatalf("pair hub endpoints = %+v, want %+v", pairRes.Hub.Endpoints, local.Endpoints)
	}
	for i, ep := range pairRes.Hub.Endpoints {
		if ep.URL != local.Endpoints[i].URL || ep.Kind != local.Endpoints[i].Kind {
			t.Fatalf("pair hub endpoint[%d] = %+v, want %+v", i, ep, local.Endpoints[i])
		}
	}

	devices, err := h.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices: %v", err)
	}
	if len(devices) != 1 || devices[0].ID != pairRes.DeviceID || devices[0].Name != "desktop-test" {
		t.Fatalf("paired devices = %+v, want one desktop-test %s", devices, pairRes.DeviceID)
	}
	if devices[0].CreatedAt == 0 || devices[0].LastSeenAt == 0 {
		t.Fatalf("device timestamps not set: %+v", devices[0])
	}

	// The device token authenticates a native client before revocation...
	if status, body := pairingDo(t, http.MethodGet, base+"/api/v1/auth/session", "",
		func(r *http.Request) {
			r.Header.Set("Origin", base)
			r.Header.Set("Authorization", "Bearer "+pairRes.DeviceToken)
		}); status != http.StatusOK || !strings.Contains(string(body), `"authenticated":true`) {
		t.Fatalf("bearer session before revoke = %d body=%s", status, body)
	}

	if err := h.RevokePairedDevice(pairRes.DeviceID); err != nil {
		t.Fatalf("RevokePairedDevice: %v", err)
	}
	devices, err = h.ListPairedDevices()
	if err != nil {
		t.Fatalf("ListPairedDevices after revoke: %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices after revoke = %+v, want none", devices)
	}
	// ...and the same token is dead afterwards.
	if status, body := pairingDo(t, http.MethodGet, base+"/api/v1/auth/session", "",
		func(r *http.Request) {
			r.Header.Set("Origin", base)
			r.Header.Set("Authorization", "Bearer "+pairRes.DeviceToken)
		}); status != http.StatusOK || strings.Contains(string(body), `"authenticated":true`) {
		t.Fatalf("bearer session after revoke = %d body=%s", status, body)
	}

	if err := h.RevokePairedDevice("does-not-exist"); err == nil {
		t.Fatal("revoking an unknown device id succeeded")
	}
	if err := h.RevokePairedDevice("  "); err == nil {
		t.Fatal("revoking an empty device id succeeded")
	}
}

// TestPairingMetadataUsesActualBoundPorts checks the metadata callback reports
// exactly the listeners that actually bound (with the OS-assigned ports), never
// a configured-but-unbound address, and that the ID/name are filled in.
func TestPairingMetadataUsesActualBoundPorts(t *testing.T) {
	dir := t.TempDir()
	h := pairingNew(t, pairingOptions(t, dir))

	// Before Start there are no endpoints, but the identity already exists.
	pre := h.hubInfo()
	if pre.ID == "" {
		t.Fatal("hub id is empty before Start")
	}
	if len(pre.Endpoints) != 0 {
		t.Fatalf("endpoints before Start = %+v, want none", pre.Endpoints)
	}

	pairingStart(t, h)
	defer pairingStop(t, h)

	urls := h.URLs()
	if len(urls) != 2 {
		t.Fatalf("URLs() = %v, want two bound listeners", urls)
	}
	info := h.hubInfo()
	if info.ID != pre.ID {
		t.Fatalf("hub id changed after Start: %q -> %q", pre.ID, info.ID)
	}
	want, _ := os.Hostname()
	if want = strings.TrimSpace(want); want == "" {
		want = fallbackHubName
	}
	if info.Name != want {
		t.Fatalf("hub name = %q, want hostname %q", info.Name, want)
	}
	if len(info.Endpoints) != len(urls) {
		t.Fatalf("endpoints = %+v, want %d entries matching URLs %v", info.Endpoints, len(urls), urls)
	}
	for i, u := range urls {
		if info.Endpoints[i].URL != u {
			t.Fatalf("endpoint[%d] = %q, want %q", i, info.Endpoints[i].URL, u)
		}
		// 127.0.0.1 is not RFC1918, so a loopback-only Hub reports "other".
		if info.Endpoints[i].Kind != "other" {
			t.Fatalf("endpoint[%d] kind = %q, want other for loopback", i, info.Endpoints[i].Kind)
		}
		if !strings.Contains(u, ":") {
			t.Fatalf("endpoint %q has no port", u)
		}
	}
	// The frozen endpoint list must not be the configured "…:0" placeholders.
	for _, ep := range info.Endpoints {
		if strings.HasSuffix(ep.URL, ":0") {
			t.Fatalf("endpoint %q reports the requested port 0 instead of the bound one", ep.URL)
		}
	}
	// api.HubInfo is the frozen shape other packages rely on.
	var _ api.HubInfo = info
}

// TestPairingEndpointKinds pins the kind classification rules.
func TestPairingEndpointKinds(t *testing.T) {
	cases := map[string]string{
		"http://192.168.1.5:8080":  "lan",
		"http://10.0.0.2:8080":     "lan",
		"http://172.16.0.1:8080":   "lan",
		"http://172.31.255.254:80": "lan",
		"http://172.32.0.1:8080":   "other",
		"http://100.64.0.1:8080":   "tailscale",
		"http://100.101.20.3:8080": "tailscale",
		"http://100.128.0.1:8080":  "other",
		"http://127.0.0.1:8080":    "other",
		"https://[::1]:8080":       "other",
		"http://192.168.0.0:0":     "lan",
		"http://hub.local:8080":    "other",
		"not a url":                "other",
	}
	for raw, want := range cases {
		if got := endpointKind(raw); got != want {
			t.Errorf("endpointKind(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestPairingHubIDStableAcrossRestart checks the hub ID persists in the data
// directory, so paired clients keep recognising the same Hub.
func TestPairingHubIDStableAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	first := pairingNew(t, pairingOptions(t, dir))
	pairingStart(t, first)
	id := first.hubInfo().ID
	if id == "" {
		t.Fatal("hub id is empty")
	}
	pairingStop(t, first)

	second := pairingNew(t, pairingOptions(t, dir))
	if got := second.hubInfo().ID; got != id {
		t.Fatalf("hub id after restart = %q, want %q", got, id)
	}
	pairingStart(t, second)
	defer pairingStop(t, second)
	if got := second.hubInfo().ID; got != id {
		t.Fatalf("hub id after restart+Start = %q, want %q", got, id)
	}
}

// TestPairingRefusesNotStartedAndStopped checks the local entry points fail
// clearly instead of touching a Hub that is not serving or is already closed.
func TestPairingRefusesNotStartedAndStopped(t *testing.T) {
	h := pairingNew(t, pairingOptions(t, t.TempDir()))
	defer pairingStop(t, h)

	if _, err := h.CreatePairingCode(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("CreatePairingCode before Start = %v, want ErrNotStarted", err)
	}
	if _, err := h.ListPairedDevices(); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("ListPairedDevices before Start = %v, want ErrNotStarted", err)
	}
	if err := h.RevokePairedDevice("x"); !errors.Is(err, ErrNotStarted) {
		t.Fatalf("RevokePairedDevice before Start = %v, want ErrNotStarted", err)
	}

	pairingStart(t, h)
	if _, err := h.CreatePairingCode(); err != nil {
		t.Fatalf("CreatePairingCode after Start: %v", err)
	}
	pairingStop(t, h)

	if _, err := h.CreatePairingCode(); !errors.Is(err, ErrClosed) {
		t.Fatalf("CreatePairingCode after Stop = %v, want ErrClosed", err)
	}
	if _, err := h.ListPairedDevices(); !errors.Is(err, ErrClosed) {
		t.Fatalf("ListPairedDevices after Stop = %v, want ErrClosed", err)
	}
	if err := h.RevokePairedDevice("x"); !errors.Is(err, ErrClosed) {
		t.Fatalf("RevokePairedDevice after Stop = %v, want ErrClosed", err)
	}
}
