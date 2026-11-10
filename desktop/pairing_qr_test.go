package main

// QR-02: the pairing QR payload/PNG helpers and the bridge field that carries
// them. Everything here is a pure function or the bound SettingsService against
// a real in-process Hub on 127.0.0.1: no Wails application and no GUI.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"strings"
	"testing"
)

func TestBuildPairingPayload(t *testing.T) {
	cases := []struct {
		name string
		code string
		urls []string
		want string
	}{
		{
			name: "single LAN URL, leading zero kept",
			code: "0421",
			urls: []string{"http://192.168.1.10:8080"},
			want: "flowdrop://pair?v=1&code=0421&url=http%3A%2F%2F192.168.1.10%3A8080",
		},
		{
			name: "no URL omits the url parameter",
			code: "0007",
			urls: nil,
			want: "flowdrop://pair?v=1&code=0007",
		},
		{
			name: "https is accepted",
			code: "1234",
			urls: []string{"https://flow.example.ts.net:8443/"},
			want: "flowdrop://pair?v=1&code=1234&url=https%3A%2F%2Fflow.example.ts.net%3A8443%2F",
		},
		{
			name: "loopback and unspecified addresses are dropped",
			code: "5678",
			urls: []string{
				"http://127.0.0.1:8080",
				"http://localhost:8080",
				"http://LOCALHOST.:8080",
				"http://[::1]:8080",
				"http://0.0.0.0:8080",
				"http://[::]:8080",
				"http://169.254.10.20:8080",
				"http://10.1.2.3:8080",
			},
			want: "flowdrop://pair?v=1&code=5678&url=http%3A%2F%2F10.1.2.3%3A8080",
		},
		{
			name: "duplicates are collapsed keeping first order",
			code: "1000",
			urls: []string{
				"http://100.101.102.103:8080",
				"http://192.168.1.10:8080",
				"http://100.101.102.103:8080",
			},
			want: "flowdrop://pair?v=1&code=1000&url=http%3A%2F%2F100.101.102.103%3A8080&url=http%3A%2F%2F192.168.1.10%3A8080",
		},
		{
			name: "unparsable and non-http URLs are dropped",
			code: "4321",
			urls: []string{"", "   ", "not a url", "/relative", "ftp://10.0.0.5", "http:///no-host", "http://10.0.0.5:80"},
			want: "flowdrop://pair?v=1&code=4321&url=http%3A%2F%2F10.0.0.5%3A80",
		},
		{
			name: "space is percent-encoded, not a plus",
			code: "1111",
			urls: []string{"http://10.0.0.5/a b"},
			want: "flowdrop://pair?v=1&code=1111&url=http%3A%2F%2F10.0.0.5%2Fa%20b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := buildPairingPayload(tc.code, tc.urls); got != tc.want {
				t.Fatalf("buildPairingPayload(%q, %v) =\n %q\nwant\n %q", tc.code, tc.urls, got, tc.want)
			}
		})
	}
}

func TestBuildPairingPayloadRejectsInvalidCode(t *testing.T) {
	for _, code := range []string{"", "123", "12345", "12a4", " 123", "123 ", "-123", "１２３４", "000\n"} {
		if got := buildPairingPayload(code, []string{"http://10.0.0.5"}); got != "" {
			t.Errorf("buildPairingPayload(%q) = %q, want empty", code, got)
		}
	}
	if got := buildPairingPayload("0421", nil); !strings.Contains(got, "code=0421") {
		t.Fatalf("leading zero lost: %q", got)
	}
}

func TestPickPairingURL(t *testing.T) {
	cases := []struct {
		name string
		urls []string
		want string
		ok   bool
	}{
		{
			name: "LAN beats Tailscale and other",
			urls: []string{"http://203.0.113.9:8080", "http://100.101.102.103:8080", "http://192.168.1.10:8080"},
			want: "http://192.168.1.10:8080",
			ok:   true,
		},
		{
			name: "all three LAN ranges rank equally, first wins",
			urls: []string{"http://192.168.1.10:8080", "http://10.1.2.3:8080", "http://172.16.5.6:8080"},
			want: "http://192.168.1.10:8080",
			ok:   true,
		},
		{
			name: "172.16/12 is LAN but 172.32 is not",
			urls: []string{"http://172.32.0.1:8080", "http://172.31.255.254:8080"},
			want: "http://172.31.255.254:8080",
			ok:   true,
		},
		{
			name: "Tailscale (100.64/10) beats other when no LAN",
			urls: []string{"http://203.0.113.9:8080", "http://100.127.0.1:8080"},
			want: "http://100.127.0.1:8080",
			ok:   true,
		},
		{
			name: "100.128 is outside 100.64/10",
			urls: []string{"http://100.128.0.1:8080", "http://203.0.113.9:8080"},
			want: "http://100.128.0.1:8080",
			ok:   true,
		},
		{
			name: "other non-loopback address",
			urls: []string{"http://203.0.113.9:8080"},
			want: "http://203.0.113.9:8080",
			ok:   true,
		},
		{
			name: "hostname ranks as other",
			urls: []string{"http://flow.example.ts.net:8080", "http://192.168.1.10:8080"},
			want: "http://192.168.1.10:8080",
			ok:   true,
		},
		{
			name: "unparsable entries are ignored",
			urls: []string{"", "nope", "ftp://10.0.0.5", "http://10.0.0.5:8080"},
			want: "http://10.0.0.5:8080",
			ok:   true,
		},
		{
			name: "only loopback",
			urls: []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:8080", "http://0.0.0.0:8080"},
			ok:   false,
		},
		{
			name: "nothing at all",
			urls: nil,
			ok:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickPairingURL(tc.urls)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("pickPairingURL(%v) = (%q, %v), want (%q, %v)", tc.urls, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestQRPNGDataURL(t *testing.T) {
	payload := buildPairingPayload("0421", []string{"http://192.168.1.10:8080"})
	dataURL, err := qrPNGDataURL(payload)
	if err != nil {
		t.Fatalf("qrPNGDataURL: %v", err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(dataURL, prefix) {
		t.Fatalf("data URL prefix = %q", dataURL[:min(len(dataURL), len(prefix)+16)])
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, prefix))
	if err != nil {
		t.Fatalf("base64 payload: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("PNG size = %v, want 256x256", img.Bounds().Size())
	}

	if _, err := qrPNGDataURL(""); err == nil {
		t.Fatal("qrPNGDataURL accepted an empty payload")
	}
}

func TestPairingCodeView(t *testing.T) {
	payload, dataURL := pairingCodeView("0073", []string{"http://127.0.0.1:8080", "http://192.168.1.10:8080"})
	if !strings.Contains(payload, "code=0073") {
		t.Fatalf("payload = %q, want the code", payload)
	}
	if strings.Contains(payload, "127.0.0.1") {
		t.Fatalf("payload leaks a loopback address: %q", payload)
	}
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Fatalf("data URL = %q", dataURL[:min(len(dataURL), 40)])
	}

	// Loopback only: the text code still works, but there is no QR to show.
	if payload, dataURL := pairingCodeView("0073", []string{"http://127.0.0.1:8080", "http://[::1]:8080"}); payload != "" || dataURL != "" {
		t.Fatalf("loopback-only pairingCodeView = (%q, %q), want empty", payload, dataURL)
	}
	if payload, dataURL := pairingCodeView("0073", nil); payload != "" || dataURL != "" {
		t.Fatalf("no-address pairingCodeView = (%q, %q), want empty", payload, dataURL)
	}
}

// TestCreatePairingCodeWithoutReachableAddress pins the bridge behaviour on a
// Hub bound to 127.0.0.1 (the test fixture): the code is created and returned,
// but no QR is offered because the loopback address is useless to a phone.
func TestCreatePairingCodeWithoutReachableAddress(t *testing.T) {
	svc, m, _ := newTestService(t)
	if urls := m.URLs(); len(urls) == 0 {
		t.Fatal("test Hub has no URLs")
	}
	code, err := svc.CreatePairingCode()
	if err != nil {
		t.Fatalf("CreatePairingCode: %v", err)
	}
	if !validPairingCode(code.Code) {
		t.Fatalf("code = %q, want four ASCII digits", code.Code)
	}
	if code.Payload != "" || code.QRDataURL != "" {
		t.Fatalf("loopback Hub returned payload=%q qr=%q, want both empty", code.Payload, code.QRDataURL)
	}

	// The QR-less DTO keeps the frozen JSON shape the settings page already
	// parses; the new fields are additive and only present when meaningful.
	raw, err := json.Marshal(code)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"payload", "qr_data_url"} {
		if strings.Contains(string(raw), unwanted) {
			t.Fatalf("empty %s still serialised: %s", unwanted, raw)
		}
	}

	// A generated code with a reachable address carries both fields; the code
	// stays a string so a leading zero survives JSON.
	payload := "flowdrop://pair?v=1&code=" + code.Code
	withQR := PairingCode{Code: code.Code, ExpiresAt: code.ExpiresAt, Payload: payload, QRDataURL: "data:image/png;base64,AA=="}
	raw, err = json.Marshal(withQR)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode PairingCode JSON: %v", err)
	}
	if decoded["payload"] != payload {
		t.Fatalf("payload field = %v, want %q", decoded["payload"], payload)
	}
	if decoded["qr_data_url"] != "data:image/png;base64,AA==" {
		t.Fatalf("qr_data_url field = %v", decoded["qr_data_url"])
	}
	if decoded["code"] != code.Code {
		t.Fatalf("code field = %v, want the four-digit string", decoded["code"])
	}
}
