package main

// QR shortcut for the local pairing code (docs/PAIRING.md, 2026-11-10).
//
// The QR code only saves typing the four digits on the phone: it is rendered
// from the code this process has just created and never travels over HTTP, is
// never logged and is never persisted. The payload format is frozen in
// docs/PAIRING.md:
//
//	flowdrop://pair?v=1&code=DDDD[&url=<percent-encoded absolute URL>]...
//
// Only the Hub's real, non-loopback listener URLs are put in it: a QR that
// points the phone at 127.0.0.1 would be useless and would leak nothing useful
// either.

import (
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"strings"

	qrcode "github.com/skip2/go-qrcode"
)

const (
	// pairingPayloadPrefix is the frozen scheme/host/version part of the payload.
	pairingPayloadPrefix = "flowdrop://pair?v=1&code="
	// pairingQRSize is the requested PNG edge length in pixels.
	pairingQRSize = 256
)

// pairingURLRank orders candidate addresses: LAN private ranges first, then the
// Tailscale/CGNAT range, then everything else that is not loopback.
const (
	pairingRankLAN = iota
	pairingRankTailscale
	pairingRankOther
	pairingRankNone
)

// buildPairingPayload renders the frozen payload for code and urls. It returns
// "" when the code is not exactly four ASCII digits, so a malformed code can
// never end up in a scannable QR. Individual URLs that are not absolute
// http/https, that point at a loopback/unspecified address or that repeat are
// dropped; the remaining order is kept. Without a usable URL the &url part is
// omitted entirely.
func buildPairingPayload(code string, urls []string) string {
	if !validPairingCode(code) {
		return ""
	}
	var b strings.Builder
	b.WriteString(pairingPayloadPrefix)
	b.WriteString(code)
	seen := make(map[string]bool, len(urls))
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		if seen[trimmed] {
			continue
		}
		if _, ok := parsePairingURL(trimmed); !ok {
			continue
		}
		seen[trimmed] = true
		b.WriteString("&url=")
		b.WriteString(percentEncodeURL(trimmed))
	}
	return b.String()
}

// pickPairingURL returns the most useful non-loopback Hub URL: a LAN private
// address first, then 100.64.0.0/10 (Tailscale), then any other non-loopback
// address, keeping the caller's order inside one rank. Entries that cannot be
// parsed, that are not http/https or that are loopback are ignored.
func pickPairingURL(urls []string) (string, bool) {
	best := ""
	bestRank := pairingRankNone
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		u, ok := parsePairingURL(trimmed)
		if !ok {
			continue
		}
		if rank := pairingURLRank(u.Hostname()); rank < bestRank {
			best, bestRank = trimmed, rank
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

// qrPNGDataURL encodes payload as a medium-error-correction PNG QR code and
// returns it as a data URL the settings page can use directly as an <img> src.
// An empty payload is a programming error and is refused instead of producing a
// scannable QR for nothing.
func qrPNGDataURL(payload string) (string, error) {
	if payload == "" {
		return "", errors.New("二维码载荷为空")
	}
	png, err := qrcode.Encode(payload, qrcode.Medium, pairingQRSize)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}

// percentEncodeURL percent-encodes a URL for the payload's query value. It is
// QueryEscape with spaces as %20 (not "+"), which a URL parser on the phone
// decodes the same way but keeps the payload unambiguous.
func percentEncodeURL(raw string) string {
	return strings.ReplaceAll(url.QueryEscape(raw), "+", "%20")
}

// validPairingCode reports whether code is exactly four ASCII digits. Leading
// zeros are significant, so the code stays a string everywhere.
func validPairingCode(code string) bool {
	if len(code) != 4 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// parsePairingURL accepts only absolute http/https URLs whose host is reachable
// from another device. A link-local or unspecified host is treated like a
// loopback host: it would never work from the phone.
func parsePairingURL(raw string) (*url.URL, bool) {
	if raw == "" {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" {
		return nil, false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, false
	}
	host := strings.ToLower(u.Hostname())
	if isLoopbackHost(host) || isLinkLocalHost(host) {
		return nil, false
	}
	return u, true
}

// pairingURLRank classifies an address for pickPairingURL. Hostnames that are
// not IP literals rank as "other": they may still be reachable (MagicDNS), but
// a literal LAN address is a safer first choice.
func pairingURLRank(host string) int {
	ip := net.ParseIP(strings.TrimSuffix(strings.ToLower(host), "."))
	if ip == nil {
		return pairingRankOther
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return pairingRankLAN
		case ip4[0] == 172 && ip4[1]&0xf0 == 16:
			return pairingRankLAN
		case ip4[0] == 192 && ip4[1] == 168:
			return pairingRankLAN
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127:
			// 100.64.0.0/10 is the shared address space Tailscale uses.
			return pairingRankTailscale
		}
	}
	return pairingRankOther
}

// isLoopbackHost matches "localhost", "*.localhost" and the loopback or
// unspecified IP literals (127.0.0.0/8, ::1, 0.0.0.0, ::).
func isLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return false
}

// isLinkLocalHost matches IPv4 169.254.0.0/16 and IPv6 fe80::/10: they are
// bound to a single link and are not a usable address for a phone.
func isLinkLocalHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLinkLocalUnicast()
}

// pairingCodeView is the QR-paired part of CreatePairingCode: the frozen code
// plus the payload/PNG derived from it. The PNG is only produced when the Hub
// has a non-loopback URL to advertise.
func pairingCodeView(code string, urls []string) (payload, dataURL string) {
	picked, ok := pickPairingURL(urls)
	if !ok {
		return "", ""
	}
	payload = buildPairingPayload(code, []string{picked})
	if payload == "" {
		return "", ""
	}
	// A PNG failure must not fail pairing: the four-digit code alone still
	// works, so the window simply shows no QR.
	dataURL, _ = qrPNGDataURL(payload)
	return payload, dataURL
}
