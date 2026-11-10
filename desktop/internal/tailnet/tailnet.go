// Package tailnet lists this machine's Tailscale peers so the settings window
// can offer 100.64.0.0/10 Hub addresses the user would otherwise have to type.
//
// It shells out to `tailscale status --json`, which needs no root and is
// present wherever the Tailscale client is installed. Nothing here is required
// for FlowDrop to work: when the command is missing, fails, or reports no
// peers, callers get an empty list and the feature quietly does nothing.
//
// A peer is only an address hint. Being in the same tailnet says nothing about
// whether a Hub runs there, so callers must probe the address and must still go
// through the Hub's own pairing flow.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// StatusTimeout bounds one `tailscale status --json` call. The command answers
// from local state and needs no network round trip, so this is generous.
const StatusTimeout = 3 * time.Second

// Peer is one other machine in the tailnet with a CGNAT (100.64/10) address.
type Peer struct {
	// Name is the peer's host name as Tailscale reports it, without the trailing
	// tailnet domain, e.g. "linux-box".
	Name string
	// IP is the first 100.64/10 IPv4 address of that peer.
	IP string
	// Online is Tailscale's own view. Offline peers are still returned, because
	// a stale report is not proof the machine is gone; the probe decides.
	Online bool
	// OS is the peer's operating system when known ("linux", "windows", ...).
	OS string
}

// errNoCommand reports that the tailscale CLI is not installed.
var errNoCommand = errors.New("tailscale 命令不可用")

// Available reports whether this machine has the tailscale CLI on PATH.
func Available() bool {
	_, err := exec.LookPath("tailscale")
	return err == nil
}

// statusJSON is the subset of `tailscale status --json` this package reads.
type statusJSON struct {
	Peer map[string]struct {
		HostName     string   `json:"HostName"`
		DNSName      string   `json:"DNSName"`
		OS           string   `json:"OS"`
		Online       bool     `json:"Online"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Peer"`
}

// ParseStatus extracts peers from one `tailscale status --json` document.
//
// Only CGNAT addresses (100.64.0.0/10) are kept: those are the addresses that
// work across networks, and the ones a LAN-collision case needs. Peers without
// such an address, and duplicate addresses, are dropped; the result is sorted
// by name for a stable UI.
func ParseStatus(raw []byte) ([]Peer, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var doc statusJSON
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(doc.Peer))
	out := make([]Peer, 0, len(doc.Peer))
	for _, p := range doc.Peer {
		ip := firstCGNAT(p.TailscaleIPs)
		if ip == "" {
			continue
		}
		if _, dup := seen[ip]; dup {
			continue
		}
		seen[ip] = struct{}{}
		out = append(out, Peer{
			Name:   displayName(p.HostName, p.DNSName, ip),
			IP:     ip,
			Online: p.Online,
			OS:     strings.ToLower(strings.TrimSpace(p.OS)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].IP < out[j].IP
	})
	return out, nil
}

// displayName prefers the host name, falls back to the DNS name without its
// tailnet domain, and finally to the address itself.
func displayName(host, dnsName, ip string) string {
	if h := strings.TrimSpace(host); h != "" {
		return h
	}
	if d := strings.TrimSpace(dnsName); d != "" {
		if short, _, ok := strings.Cut(strings.TrimSuffix(d, "."), "."); ok {
			return short
		}
		return strings.TrimSuffix(d, ".")
	}
	return ip
}

// firstCGNAT returns the first Tailscale (100.64.0.0/10) IPv4 address in ips.
func firstCGNAT(ips []string) string {
	for _, raw := range ips {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil {
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil || !IsCGNAT(ip4) {
			continue
		}
		return ip4.String()
	}
	return ""
}

// IsCGNAT reports whether ip4 is inside 100.64.0.0/10, the range Tailscale
// hands out. It is deliberately narrow: 100.64/10 is carrier-grade NAT space,
// so an address there is a Tailscale peer in practice.
func IsCGNAT(ip4 net.IP) bool {
	if ip4 == nil || len(ip4) != net.IPv4len {
		return false
	}
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// Peers returns this machine's tailnet peers. A missing CLI is reported as
// errNoCommand so callers can hide the feature instead of showing an error.
func Peers(ctx context.Context) ([]Peer, error) {
	if !Available() {
		return nil, errNoCommand
	}
	ctx, cancel := context.WithTimeout(ctx, StatusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tailscale", "status", "--json")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return ParseStatus(out)
}

// ErrUnavailable reports whether err means the feature cannot run here at all
// (no CLI). Callers use it to stay silent rather than warn.
func ErrUnavailable(err error) bool { return errors.Is(err, errNoCommand) }
