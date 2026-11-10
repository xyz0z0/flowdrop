// Package lanbrowse finds FlowDrop Hubs advertised on the local network over
// DNS-SD (mDNS).
//
// The Hub publishes itself with the record contract documented in
// server/internal/discovery and mirror-implemented by Android's
// DiscoveryProtocol:
//
//	service type  _flowdrop._tcp.local.
//	instance      FlowDrop-<sanitised host nickname>
//	SRV           the Hub's real HTTP port
//	address       an explicit RFC1918 IPv4 the Hub is actually bound to
//	TXT           v=1, scheme=http, path=/
//
// Nothing worse than a LAN address and a port is learned here: a discovery
// result is an address hint, never proof of identity, so callers must still
// reach the Hub's own login/pairing flow.
//
// mDNS is link-local. This package can only find Hubs on the same layer-2
// network, which is why the settings window also offers Tailscale peers
// (desktop/internal/tailnet) as a separate source.
package lanbrowse

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/grandcat/zeroconf"

	"flowdrop/desktop/internal/network"
)

const (
	// ServiceType is the DNS-SD service type without the trailing domain.
	ServiceType = "_flowdrop._tcp"
	// Domain is the DNS-SD domain the Hub advertises in.
	Domain = "local."
	// InstancePrefix is the instance-name prefix every Hub uses.
	InstancePrefix = "FlowDrop-"
	// DefaultBrowseTimeout bounds one browse when the caller passes no deadline
	// of its own: long enough for one multicast round trip plus a retry, short
	// enough that the settings window is not left waiting.
	DefaultBrowseTimeout = 3 * time.Second
)

// Candidate is one reachable-looking Hub address found on the local network.
type Candidate struct {
	// URL is always plain http:// with the advertised host and port.
	URL string
	// Host is the advertised IPv4 address, without the scheme or port.
	Host string
	// Port is the advertised Hub port.
	Port int
	// Instance is the DNS-SD instance name, e.g. "FlowDrop-bob-pc".
	Instance string
}

// TXT keys, matching the Hub's publisher and Android's DiscoveryProtocol.
const (
	txtVersion = "v"
	txtScheme  = "scheme"
	txtPath    = "path"

	requiredVersion = "1"
	requiredScheme  = "http"
	requiredPath    = "/"
)

// Parse validates one resolved DNS-SD record and turns it into a candidate.
//
// It rejects anything that is not exactly a FlowDrop Hub advertisement: a
// different service type, an instance name without the FlowDrop- prefix, a
// non-HTTP TXT record, an impossible port or a host that is not an RFC1918
// IPv4. An empty TXT map is tolerated, because older publishers may omit it;
// a TXT map that is present must match.
func Parse(service, instance string, port int, host string, txt []string) (Candidate, bool) {
	if !strings.EqualFold(normalizeService(service), ServiceType) {
		return Candidate{}, false
	}
	if !strings.HasPrefix(instance, InstancePrefix) {
		return Candidate{}, false
	}
	if port <= 0 || port > 65535 {
		return Candidate{}, false
	}
	if !txtMatches(txt) {
		return Candidate{}, false
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return Candidate{}, false
	}
	ip4 := ip.To4()
	if ip4 == nil || !network.IsLAN(ip4) {
		// Loopback, Tailscale/CGNAT, link-local, IPv6 and public addresses are
		// never announced as LAN services, so they cannot be one.
		return Candidate{}, false
	}
	return Candidate{
		URL:      fmt.Sprintf("http://%s:%d", ip4.String(), port),
		Host:     ip4.String(),
		Port:     port,
		Instance: instance,
	}, true
}

// normalizeService strips the trailing dot and the domain so "_flowdrop._tcp.",
// "_flowdrop._tcp" and "_flowdrop._tcp.local." all compare equal.
func normalizeService(service string) string {
	s := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(service)), ".")
	s = strings.TrimSuffix(s, strings.TrimSuffix(strings.ToLower(Domain), "."))
	return strings.TrimSuffix(s, ".")
}

// txtMatches enforces v=1, scheme=http, path=/ when the record carries TXT
// data. Values are compared case-insensitively and surrounding whitespace is
// ignored, because mDNS TXT strings are free-form.
func txtMatches(txt []string) bool {
	if len(txt) == 0 {
		return true
	}
	got := map[string]string{}
	for _, entry := range txt {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		got[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	if got[txtVersion] != requiredVersion {
		return false
	}
	if strings.ToLower(got[txtScheme]) != requiredScheme {
		return false
	}
	return got[txtPath] == requiredPath
}

// Browse looks for FlowDrop Hubs on the local network and returns the
// candidates it found, deduplicated by URL and sorted for a stable list. A
// cancelled or expired ctx is not an error: whatever was collected so far is
// returned, because an empty network is a normal answer, not a failure.
func Browse(ctx context.Context, timeout time.Duration) ([]Candidate, error) {
	if timeout <= 0 {
		timeout = DefaultBrowseTimeout
	}
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, fmt.Errorf("无法启动 mDNS 浏览：%w", err)
	}
	entries := make(chan *zeroconf.ServiceEntry, 16)
	collected := make(map[string]Candidate)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			for _, c := range candidatesOf(entry) {
				if _, seen := collected[c.URL]; !seen {
					collected[c.URL] = c
				}
			}
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Browse only reports the initial query: it returns once that query went out
	// and keeps streaming responses until ctx ends. A failed initial query means
	// this machine cannot browse at all (no multicast interface), which is worth
	// reporting instead of pretending the network is empty.
	if err := resolver.Browse(ctx, ServiceType, Domain, entries); err != nil {
		cancel()
		<-done
		return nil, fmt.Errorf("局域网发现不可用：%w", err)
	}
	// Wait out the browse window; zeroconf closes the entries channel when ctx
	// ends, which is what stops the collector goroutine.
	<-ctx.Done()
	<-done

	out := make([]Candidate, 0, len(collected))
	for _, c := range collected {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out, nil
}

// candidatesOf turns one resolved mDNS entry into candidates, one per explicit
// RFC1918 IPv4 address it carries. Advertised host names are ignored on
// purpose: the Hub only ever publishes addresses it is bound to, and a name
// would need a second resolution step we cannot verify.
func candidatesOf(entry *zeroconf.ServiceEntry) []Candidate {
	if entry == nil {
		return nil
	}
	out := make([]Candidate, 0, len(entry.AddrIPv4))
	for _, ip := range entry.AddrIPv4 {
		c, ok := Parse(entry.Service, entry.Instance, entry.Port, ip.String(), entry.Text)
		if !ok {
			continue
		}
		out = append(out, c)
	}
	return out
}

// String renders a candidate the way the settings window shows it, e.g.
// "http://192.168.1.20:8080（FlowDrop-bob-pc）".
func (c Candidate) String() string {
	if c.Instance == "" {
		return c.URL
	}
	return c.URL + "（" + c.Instance + "）"
}
