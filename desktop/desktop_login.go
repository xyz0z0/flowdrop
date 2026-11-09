package main

// Desktop self-pairing helpers.
//
// The inbox window loads the Hub's real HTTP origin, so it only ever holds an
// HttpOnly session cookie with the Hub's normal TTL. The shell therefore acts
// as one more native client: it asks the local Hub for a four-digit pairing
// code, trades it for a long-lived device token (POST /api/v1/auth/pair with
// native=true), stores that token in desktop.json and, on every page load,
// injects a small bootstrap script that replays the token as
// "Authorization: Bearer" against GET /api/v1/auth/session. The Hub reissues
// the cookie and the user never sees a pairing code again.
//
// main.go wires this in for local mode only: main() calls ensureDesktopLogin
// before the windows exist, and the inbox window's JS injection is the single
// path the token takes into the page. The helpers themselves never log, never
// include the pairing code or the token in an error and never start a HTTP
// client of their own (the caller owns timeouts).

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"flowdrop/desktop/internal/appsettings"
)

// pairRequestPath and sessionPath are the frozen Hub endpoints from
// docs/PAIRING.md.
const (
	pairRequestPath = "/api/v1/auth/pair"
	sessionPath     = "/api/v1/auth/session"
)

// maxPairResponseBytes bounds the pairing response we are willing to parse.
const maxPairResponseBytes = 1 << 20

// desktopLoginTimeout bounds the shell's own pairing request. It only exists so
// a Hub that accepts the connection but never answers cannot hang startup: the
// request is one POST to loopback/LAN and either finishes in milliseconds or
// fails into the "pair by code" fallback.
const desktopLoginTimeout = 10 * time.Second

// The inbox bootstrap's reload budget (see desktopBootstrapJS): sessionStorage
// key and the number of reloads one page load chain may trigger before the
// script gives up. Two is enough to survive "cookie just set, page still on the
// pre-login render" and small enough that a Hub which never keeps the cookie
// cannot make the window unusable.
const (
	bootstrapReloadKey  = "flowdropBootstrapReloads"
	bootstrapMaxReloads = 2
)

// desktopDeviceName returns the name this machine shows up under in the Hub's
// device list, for example "本机桌面 · bob-laptop". It is stable for a given
// host so re-pairing updates the same device entry instead of creating
// duplicates.
func desktopDeviceName() string {
	host, err := os.Hostname()
	host = strings.TrimSpace(host)
	if err != nil || host == "" {
		host = "未知主机"
	}
	return "本机桌面 · " + host
}

// hubBaseURL validates and normalises the Hub base URL the pairing request is
// sent to. Only a bare http(s) origin with a host is accepted, because the
// result is also the origin the inbox window later loads and the place the
// device token is sent to.
func hubBaseURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("pair desktop device: hub base URL is empty")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("pair desktop device: hub base URL is not a valid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("pair desktop device: hub base URL must start with http:// or https://, got scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("pair desktop device: hub base URL %q has no host", trimmed)
	}
	return strings.TrimRight(trimmed, "/"), nil
}

// pairErrorBody is the error body of the frozen {error:{code,message}} shape.
type pairErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// pairResponse is the subset of the Hub's sessionResponse this helper needs.
type pairResponse struct {
	DeviceID    string `json:"device_id"`
	DeviceToken string `json:"device_token"`
}

// pairDesktopDevice registers this desktop as a native device using a one-time
// pairing code generated on the same machine. It returns the Hub's device ID
// and the long-lived device token; neither the code nor the token ever appears
// in an error, so the result is safe to log.
func pairDesktopDevice(hc *http.Client, baseURL, deviceName, code string) (deviceID, deviceToken string, err error) {
	base, err := hubBaseURL(baseURL)
	if err != nil {
		return "", "", err
	}
	if hc == nil {
		return "", "", errors.New("pair desktop device: an http client is required")
	}

	body, err := json.Marshal(struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
		Native     bool   `json:"native"`
	}{Code: code, DeviceName: deviceName, Native: true})
	if err != nil {
		return "", "", fmt.Errorf("pair desktop device: encode request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, base+pairRequestPath, bytes.NewReader(body))
	if err != nil {
		// url.Error would quote the URL, which is the Hub address, never the
		// code or a token.
		return "", "", fmt.Errorf("pair desktop device: build request: %w", err)
	}
	// Deliberately no Origin header: the Hub treats a request without Origin as
	// a non-browser client (see originAllowed) instead of rejecting it for
	// being a foreign origin.
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		// The transport error may quote the URL; it never carries the body.
		return "", "", fmt.Errorf("pair desktop device: request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPairResponseBytes))
	if err != nil {
		return "", "", fmt.Errorf("pair desktop device: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Only the Hub's own error code is echoed, and only in the shape the
		// frozen contract allows, so a broken server cannot smuggle the
		// submitted pairing code into our error text.
		var pe pairErrorBody
		if json.Unmarshal(raw, &pe) == nil && isOpaqueErrorCode(pe.Error.Code) {
			return "", "", fmt.Errorf("pair desktop device: hub returned %s (error code %s)", resp.Status, pe.Error.Code)
		}
		return "", "", fmt.Errorf("pair desktop device: hub returned %s", resp.Status)
	}

	var pr pairResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		return "", "", fmt.Errorf("pair desktop device: decode response: %w", err)
	}
	if pr.DeviceToken == "" {
		return "", "", errors.New("pair desktop device: hub response has no device_token (native pairing refused)")
	}
	if pr.DeviceID == "" {
		return "", "", errors.New("pair desktop device: hub response has no device_id")
	}
	return pr.DeviceID, pr.DeviceToken, nil
}

// pairLocalHubDevice makes this window a paired device of the local Hub when it
// is not one yet: it asks newCode for a one-time pairing code and trades it for
// a native device token through pairDesktopDevice.
//
// It deliberately sends no request when a token is already saved (the stored
// token is the long-lived credential; a second pairing would only add a
// duplicate device record), and it never writes the settings file itself: the
// caller decides whether a partly learned identity should be persisted. Neither
// the code nor the token appears in the returned error.
func pairLocalHubDevice(hc *http.Client, hubURL, deviceName string, st appsettings.Settings, newCode func() (string, error)) (appsettings.Settings, error) {
	if st.DesktopDeviceToken != "" {
		return st, nil
	}
	if newCode == nil {
		return st, errors.New("pair local hub device: a pairing code source is required")
	}
	code, err := newCode()
	if err != nil {
		return st, fmt.Errorf("pair local hub device: no pairing code: %w", err)
	}
	deviceID, token, err := pairDesktopDevice(hc, hubURL, deviceName, code)
	if err != nil {
		// pairDesktopDevice already keeps the code and the token out of its
		// errors; the settings keep whatever they had.
		return st, err
	}
	next := st
	next.DesktopDeviceID = deviceID
	next.DesktopDeviceToken = token
	return next, nil
}

// newPairingCode asks the running local Hub for one fresh four-digit code. The
// code only lives in this process: it is never logged and never crosses a
// network boundary of its own.
func (d *desktopState) newPairingCode() (string, error) {
	if d.manager == nil {
		return "", errors.New("no local hub manager")
	}
	code, err := d.manager.CreatePairingCode()
	if err != nil {
		return "", err
	}
	return code.Code, nil
}

// desktopDeviceToken returns the token the inbox page replays, or "" when this
// launch has none (remote mode, or a pairing that failed).
func (d *desktopState) desktopDeviceToken() string {
	v, _ := d.deviceToken.Load().(string)
	return v
}

// setDeviceToken publishes the token for the inbox window. It is stored before
// any window exists and read when the page is injected, so plain field access
// would be a data race.
func (d *desktopState) setDeviceToken(token string) {
	d.deviceToken.Store(token)
}

// ensureDesktopLogin pairs this window with the local Hub before the windows
// are created, so the inbox opens already logged in instead of asking for a
// code. It never blocks or aborts the start: every failure is one log line and
// the unchanged settings, and the inbox can still be paired by hand.
//
// Remote mode and a shell without a Hub are left untouched: the remote Hub owns
// its devices, and there is no local pairing API to call.
func (d *desktopState) ensureDesktopLogin(st appsettings.Settings) appsettings.Settings {
	if d.manager == nil || st.IsRemote() {
		return st
	}
	hubURL := preferredHubURL(d.manager)
	if hubURL == "" {
		return st
	}
	client := d.loginClient
	if client == nil {
		client = &http.Client{Timeout: desktopLoginTimeout}
	}
	next, err := pairLocalHubDevice(client, hubURL, desktopDeviceName(), st, d.newPairingCode)
	if err != nil {
		log.Printf("desktop login: 无法与本地 Hub 免码配对：%v；收件箱仍可用配对码手动登录", err)
		return st
	}
	// Publish the token even when it was already saved: this is what the inbox
	// window injects, and a restart must reuse it without a new pairing.
	d.setDeviceToken(next.DesktopDeviceToken)
	if next.DesktopDeviceToken == st.DesktopDeviceToken {
		return st
	}
	// A saved device ID that is not the one we just got is a leftover record of
	// an earlier identity (for example a token the Hub rejected and the loader
	// dropped). Revoke it only now, once the new pairing really worked, so a
	// failed pairing never leaves the window without a device.
	if st.DesktopDeviceID != "" && st.DesktopDeviceID != next.DesktopDeviceID {
		if rerr := d.manager.RevokePairedDevice(st.DesktopDeviceID); rerr != nil {
			log.Printf("desktop login: 无法撤销旧的设备记录 %q：%v", st.DesktopDeviceID, rerr)
		}
	}
	if serr := appsettings.Save(d.settingsPath, next); serr != nil {
		log.Printf("desktop login: 免码登录令牌未能写入 %s：%v；本次运行仍可使用", d.settingsPath, serr)
	}
	// No token, no device ID and no code in this line.
	log.Printf("desktop login: 本机窗口已作为 %q 与本地 Hub 配对", desktopDeviceName())
	return next
}

// isOpaqueErrorCode reports whether a Hub error code is a plain identifier, so
// echoing it cannot leak request data back into our error message.
func isOpaqueErrorCode(code string) bool {
	if code == "" || len(code) > 64 {
		return false
	}
	for _, r := range code {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// desktopBootstrapJS returns the script the shell injects into the inbox page.
// It is empty when there is no token to replay, so an unpaired shell injects
// nothing at all.
//
// The script makes at most two requests and never retries:
//   - GET /api/v1/auth/session with cookies: if the Hub still accepts the
//     session cookie there is nothing to do.
//   - otherwise the same endpoint with "Authorization: Bearer <token>", which
//     makes the Hub reissue the cookie for this origin; only then does it
//     reload the page.
//
// The reload is bounded: a Hub that answers the Bearer request but does not keep
// the cookie it reissued (a Secure cookie on a plain-HTTP origin, a cookie
// policy, third-party blocking) would otherwise reload this page forever,
// leaving it unusable. The script therefore keeps a reload budget in
// sessionStorage ("flowdropBootstrapReloads", at most 2) that only a page load
// which is already authenticated resets; once the budget is used up, or when
// sessionStorage itself is unavailable (private mode throws), it stops quietly
// instead of reloading again. The page then behaves like an unpaired one: the
// normal pairing form is still right there.
//
// The token is embedded through encoding/json, so quotes, backslashes and
// "</script>" cannot break out of the string literal (Go's encoder escapes
// '<', '>' and '&'). Every failure is swallowed: no alert, no console noise
// and no reload loop.
func desktopBootstrapJS(token string) string {
	if token == "" {
		return ""
	}
	tokenLiteral, err := json.Marshal(token)
	if err != nil {
		// A string always marshals; be safe rather than inject something odd.
		return ""
	}
	var b strings.Builder
	b.WriteString("(function () {\n")
	b.WriteString("  if (window.__flowdropDesktopBootstrap) { return; }\n")
	b.WriteString("  window.__flowdropDesktopBootstrap = true;\n")
	b.WriteString("  var token = ")
	b.Write(tokenLiteral)
	b.WriteString(";\n")
	b.WriteString("  var url = \"")
	b.WriteString(sessionPath)
	b.WriteString("\";\n")
	b.WriteString("  var reloadKey = \"")
	b.WriteString(bootstrapReloadKey)
	b.WriteString("\";\n")
	b.WriteString("  var maxReloads = ")
	b.WriteString(strconv.Itoa(bootstrapMaxReloads))
	b.WriteString(";\n")
	b.WriteString("  function reloadsUsed() {\n")
	b.WriteString("    try {\n")
	b.WriteString("      var used = parseInt(window.sessionStorage.getItem(reloadKey), 10);\n")
	b.WriteString("      return isNaN(used) || used < 0 ? 0 : used;\n")
	b.WriteString("    } catch (e) {\n")
	b.WriteString("      // No sessionStorage: nothing can bound the reloads, so never reload.\n")
	b.WriteString("      return -1;\n")
	b.WriteString("    }\n")
	b.WriteString("  }\n")
	b.WriteString("  function clearReloads() {\n")
	b.WriteString("    try { window.sessionStorage.removeItem(reloadKey); } catch (e) {}\n")
	b.WriteString("  }\n")
	b.WriteString("  function reloadOnce() {\n")
	b.WriteString("    var used = reloadsUsed();\n")
	b.WriteString("    if (used < 0 || used >= maxReloads) { return; }\n")
	b.WriteString("    try {\n")
	b.WriteString("      window.sessionStorage.setItem(reloadKey, String(used + 1));\n")
	b.WriteString("    } catch (e) { return; }\n")
	b.WriteString("    location.reload();\n")
	b.WriteString("  }\n")
	b.WriteString("  fetch(url, { credentials: \"same-origin\" })\n")
	b.WriteString("    .then(function (response) {\n")
	b.WriteString("      return response.ok ? response.json() : null;\n")
	b.WriteString("    })\n")
	b.WriteString("    .then(function (body) {\n")
	b.WriteString("      if (body && body.authenticated === true) {\n")
	b.WriteString("        // The cookie is really in place: nothing to do, and the next\n")
	b.WriteString("        // session expiry starts again with a full reload budget.\n")
	b.WriteString("        clearReloads();\n")
	b.WriteString("        return;\n")
	b.WriteString("      }\n")
	b.WriteString("      return fetch(url, {\n")
	b.WriteString("        credentials: \"same-origin\",\n")
	b.WriteString("        headers: { Authorization: \"Bearer \" + token }\n")
	b.WriteString("      }).then(function (response) {\n")
	b.WriteString("        return response.ok ? response.json() : null;\n")
	b.WriteString("      }).then(function (renewed) {\n")
	b.WriteString("        // No clearReloads() here: this reload must use up the budget,\n")
	b.WriteString("        // otherwise the count restarts and never caps the loop.\n")
	b.WriteString("        if (renewed && renewed.authenticated === true) { reloadOnce(); }\n")
	b.WriteString("      });\n")
	b.WriteString("    })\n")
	b.WriteString("    .catch(function () {});\n")
	b.WriteString("})();\n")
	return b.String()
}
