// Package discovery publishes the FlowDrop Hub as a DNS-SD (mDNS) service so
// clients on the same trusted LAN can find the plaintext HTTP entry point
// without being told an IP address.
//
// The published record is deliberately narrow (see docs/VERIFICATION.md):
//
//	service type  _flowdrop._tcp.local.
//	instance      FlowDrop-<sanitised host nickname>[-N per extra address]
//	SRV           the real Hub port of that listener
//	address       only a specific RFC1918 IPv4 the Hub is actually bound to
//	TXT           v=1, scheme=http, path=/
//
// No password, session, item content or filesystem path is ever placed in the
// broadcast. Only plaintext HTTP LAN entries (cookie_secure=false) are
// advertised: loopback, wildcard, Tailscale/CGNAT, link-local, public and
// reverse-proxied HTTPS listeners are not presented as a LAN service, and an
// interface that the Hub does not listen on is never announced.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	// ServiceType is the DNS-SD service type without the domain.
	ServiceType = "_flowdrop._tcp"
	// Domain is the DNS-SD domain.
	Domain = "local."
	// ServiceTypeFQDN is the fully qualified service type.
	ServiceTypeFQDN = ServiceType + "." + Domain

	// MaxInstanceLabel is the RFC 6762/6763 limit for a single DNS label.
	MaxInstanceLabel = 63

	// browseTimeout bounds the best-effort pre-registration name conflict
	// scan. It does not block the HTTP server: registration happens before
	// serving starts, and failure here only affects the chosen suffix.
	browseTimeout = 1200 * time.Millisecond
)

// TXT returns the exact TXT record values required by the frozen protocol.
func TXT() []string {
	return []string{"v=1", "scheme=http", "path=/"}
}

var (
	// ErrDisabled is returned when the caller explicitly switched discovery off.
	ErrDisabled = errors.New("LAN discovery is disabled by configuration")
	// ErrIneligible marks a configuration where publishing would be wrong
	// (loopback, wildcard, Tailscale, HTTPS-only, non-LAN address, ...).
	ErrIneligible = errors.New("listen address is not eligible for LAN discovery")
)

// Target is the validated network endpoint that may be advertised.
type Target struct {
	IP    net.IP
	Port  int
	Iface net.Interface
}

// Publisher is the narrow lifecycle interface implemented by the mDNS
// responder. It is an interface so lifecycle tests do not need a real socket.
type Publisher interface {
	Shutdown()
}

type ifaceAddr struct {
	Iface net.Interface
	IPs   []net.IP
}

// Options configures Start. The unexported fields are test seams; production
// callers only need Enabled, Listen, AdditionalListen, CookieSecure and Logger.
type Options struct {
	// Enabled is the resolved discovery_enabled value (default true).
	Enabled bool
	// Listen is the primary bound address.
	Listen string
	// AdditionalListen lists the other bound addresses of the same Hub. Only
	// eligible RFC1918 plain-HTTP listeners are advertised; loopback,
	// wildcard and Tailscale/CGNAT entries are skipped with a reason.
	AdditionalListen []string
	CookieSecure     bool
	Logger           *slog.Logger
	// Hostname overrides the OS hostname (tests/documentation).
	Hostname string

	ifaces   []ifaceAddr
	browse   func(ctx context.Context, iface net.Interface) ([]string, error)
	register func(instance string, t Target, host string, txt []string, iface net.Interface, log *slog.Logger) (Publisher, error)
}

// Handle owns the registered mDNS services. Close withdraws every service
// (TTL 0), sends the goodbye announcements and releases the UDP 5353 socket(s).
type Handle struct {
	pubs      []Publisher
	log       *slog.Logger
	instances []string
	once      sync.Once
}

// Close gracefully unregisters every published service. It is safe to call
// more than once and is a no-op on a nil handle.
func (h *Handle) Close() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		for i, pub := range h.pubs {
			pub.Shutdown()
			h.log.Info("mDNS discovery withdrawn", "instance", h.instances[i], "service", ServiceTypeFQDN)
		}
	})
}

// Start validates the listen configuration and, for every eligible address,
// registers a FlowDrop DNS-SD service on the interface that owns the bound IPv4.
// Ineligible additional addresses are skipped (the primary keeps the historical
// single-address error), so a LAN + Tailscale Hub advertises only the LAN entry.
//
// A publishing error (for example UDP 5353 unavailable) is returned to the
// caller after already-published records are withdrawn, so the Hub can log it
// and keep serving normal HTTP traffic.
func Start(ctx context.Context, opts Options) (*Handle, error) {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	if !opts.Enabled {
		return nil, ErrDisabled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ifaces := opts.ifaces
	if ifaces == nil {
		var err error
		ifaces, err = localInterfaces()
		if err != nil {
			return nil, fmt.Errorf("%w: enumerate interfaces: %v", ErrIneligible, err)
		}
	}

	listens := uniqueListens(opts.Listen, opts.AdditionalListen)
	multi := len(listens) > 1
	targets := make([]Target, 0, len(listens))
	var reasons []string
	for _, listen := range listens {
		target, err := evaluate(listen, opts.CookieSecure, ifaces)
		if err != nil {
			if !multi {
				return nil, err
			}
			reasons = append(reasons, fmt.Sprintf("%s: %v", listen, err))
			log.Info("mDNS LAN discovery not published for this listen address", "listen", listen, "reason", err)
			continue
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrIneligible, strings.Join(reasons, "; "))
	}

	hostname := opts.Hostname
	if hostname == "" {
		var err error
		hostname, err = os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("determine hostname for mDNS instance: %w", err)
		}
	}
	nick := SanitizeNickname(hostname)

	browse := opts.browse
	if browse == nil {
		browse = browseExisting
	}
	register := opts.register
	if register == nil {
		register = defaultRegister
	}
	txt := TXT()

	// Browse every target interface so instance names stay unique across the
	// whole Hub, then register one responder per eligible listener/interface.
	chosen := make([]string, 0, len(targets))
	h := &Handle{log: log}
	for i, target := range targets {
		if err := ctx.Err(); err != nil {
			h.Close()
			return nil, err
		}
		existing, browseErr := browse(ctx, target.Iface)
		if browseErr != nil {
			// Conflict detection is best-effort; never block publishing on it.
			log.Debug("mDNS name conflict scan failed; using base instance name", "err", browseErr)
		}
		chosen = append(chosen, existing...)
		instance := UniqueInstanceName(nick, chosen)
		chosen = append(chosen, instance)
		if err := ctx.Err(); err != nil {
			h.Close()
			return nil, err
		}

		host := advertisedHost(nick, i, len(targets))
		pub, err := register(instance, target, host, txt, target.Iface, log)
		if err != nil {
			// Never leave a partial multi-address announcement behind.
			h.Close()
			return nil, fmt.Errorf("register mDNS service: %w", err)
		}
		// Records are published with a 120 s TTL (RFC 6762 recommendation): a
		// crashed Hub disappears from client caches within two minutes even if
		// the graceful goodbye packet was missed.
		h.pubs = append(h.pubs, pub)
		h.instances = append(h.instances, instance)
		log.Info("mDNS discovery published",
			"service", ServiceTypeFQDN,
			"instance", instance,
			"host", host,
			"address", target.IP.String(),
			"port", target.Port,
			"interface", target.Iface.Name,
			"txt", txt,
		)
	}
	return h, nil
}

func uniqueListens(primary string, additional []string) []string {
	out := make([]string, 0, 1+len(additional))
	seen := make(map[string]struct{}, 1+len(additional))
	for _, listen := range append([]string{primary}, additional...) {
		if _, ok := seen[listen]; ok {
			continue
		}
		seen[listen] = struct{}{}
		out = append(out, listen)
	}
	return out
}

// advertisedHost keeps the historical SRV target for a single listener. With
// multiple eligible listeners every extra target gets its own host label so
// two A records on the same interface cannot claim the same mDNS name.
func advertisedHost(nick string, index, total int) string {
	suffix := ""
	if index > 0 && total > 1 {
		suffix = fmt.Sprintf("-%d", index+1)
	}
	limit := MaxInstanceLabel - len("flowdrop-") - len(suffix)
	if len(nick) > limit {
		nick = strings.TrimRight(nick[:limit], "-")
	}
	if nick == "" {
		nick = "Hub"
	}
	return strings.ToLower("flowdrop-" + nick + suffix + "." + Domain)
}

// Inspect validates a listen address without touching the network. It is used
// by `flowdrop doctor` to explain whether discovery would be published.
func Inspect(listen string, cookieSecure bool) (Target, error) {
	ifaces, err := localInterfaces()
	if err != nil {
		return Target{}, fmt.Errorf("%w: enumerate interfaces: %v", ErrIneligible, err)
	}
	return evaluate(listen, cookieSecure, ifaces)
}

func localInterfaces() ([]ifaceAddr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	out := make([]ifaceAddr, 0, len(ifaces))
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		var ips []net.IP
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil {
				ips = append(ips, ip.To4())
			}
		}
		out = append(out, ifaceAddr{Iface: iface, IPs: ips})
	}
	return out, nil
}

func evaluate(listen string, cookieSecure bool, ifaces []ifaceAddr) (Target, error) {
	host, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return Target{}, ineligible("listen %q is not host:port: %v", listen, err)
	}
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		return Target{}, ineligible("listen address is a wildcard (%s); bind the specific LAN IP instead", host)
	}
	if ip == nil {
		return Target{}, ineligible("listen host %q is not a literal IP; mDNS advertises the bound interface address only", host)
	}
	if ip.IsLoopback() {
		return Target{}, ineligible("listen address is loopback; there is no LAN entry to advertise")
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return Target{}, ineligible("listen address %s is not IPv4; only a specific RFC1918 LAN IPv4 entry is advertised", host)
	}
	if err := classifyUnadvertisable(ip4); err != nil {
		return Target{}, err
	}
	if cookieSecure {
		return Target{}, ineligible("cookie_secure=true: discovery only publishes a directly usable plaintext HTTP LAN entry")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return Target{}, ineligible("listen port %q is invalid", portStr)
	}

	var match *ifaceAddr
	for i := range ifaces {
		for _, candidate := range ifaces[i].IPs {
			if candidate.Equal(ip4) {
				match = &ifaces[i]
				break
			}
		}
		if match != nil {
			break
		}
	}
	if match == nil {
		return Target{}, ineligible("listen address %s is not assigned to any local interface", host)
	}
	if match.Iface.Flags&net.FlagUp == 0 {
		return Target{}, ineligible("interface %s is down", match.Iface.Name)
	}
	if match.Iface.Flags&net.FlagMulticast == 0 {
		return Target{}, ineligible("interface %s does not support multicast", match.Iface.Name)
	}

	return Target{IP: ip4, Port: port, Iface: match.Iface}, nil
}

func classifyUnadvertisable(ip4 net.IP) error {
	if !isRFC1918(ip4) {
		switch {
		case isTailscaleCGNAT(ip4):
			return ineligible("address %s is Tailscale/CGNAT; Tailscale access is not advertised as a LAN entry", ip4)
		case ip4[0] == 169 && ip4[1] == 254:
			return ineligible("address %s is IPv4 link-local; not a routable LAN entry", ip4)
		default:
			return ineligible("address %s is not RFC1918 private; refusing to advertise a public address", ip4)
		}
	}
	return nil
}

func ineligible(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrIneligible, fmt.Sprintf(format, args...))
}

func isRFC1918(ip4 net.IP) bool {
	if ip4 == nil || ip4.To4() == nil {
		return false
	}
	return ip4[0] == 10 ||
		(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
		(ip4[0] == 192 && ip4[1] == 168)
}

func isTailscaleCGNAT(ip4 net.IP) bool {
	// 100.64.0.0/10 shared address space; Tailscale uses it for tailnet IPs.
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

// SanitizeNickname converts an OS hostname into a safe DNS-SD instance label
// component: ASCII alphanumerics and hyphens only, no leading/trailing hyphen,
// truncated so "FlowDrop-"+nick fits a 63-byte DNS label.
func SanitizeNickname(hostname string) string {
	hostname = strings.TrimSpace(hostname)
	// mDNS hostnames normally carry a domain suffix; the nickname is the first label.
	if i := strings.IndexByte(hostname, '.'); i >= 0 {
		hostname = hostname[:i]
	}

	var b strings.Builder
	lastHyphen := false
	for _, r := range hostname {
		safe := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if safe {
			b.WriteRune(r)
			lastHyphen = false
			continue
		}
		if b.Len() > 0 && !lastHyphen {
			b.WriteByte('-')
			lastHyphen = true
		}
	}
	nick := strings.Trim(b.String(), "-")
	if nick == "" {
		nick = "Hub"
	}
	limit := MaxInstanceLabel - len("FlowDrop-")
	if len(nick) > limit {
		nick = strings.TrimRight(nick[:limit], "-")
	}
	if nick == "" {
		nick = "Hub"
	}
	return nick
}

// InstanceName builds FlowDrop-<nick> with an optional conflict suffix. The
// total DNS label never exceeds MaxInstanceLabel bytes.
func InstanceName(nick string, suffix int) string {
	extra := ""
	if suffix > 1 {
		extra = fmt.Sprintf("-%d", suffix)
	}
	limit := MaxInstanceLabel - len("FlowDrop-") - len(extra)
	if len(nick) > limit {
		nick = strings.TrimRight(nick[:limit], "-")
	}
	if nick == "" {
		nick = "Hub"
	}
	return "FlowDrop-" + nick + extra
}

// UniqueInstanceName picks the first name not already present in existing.
// DNS-SD instance names are compared case-insensitively.
func UniqueInstanceName(nick string, existing []string) string {
	taken := make(map[string]struct{}, len(existing))
	for _, name := range existing {
		taken[strings.ToLower(name)] = struct{}{}
	}
	for suffix := 1; suffix <= 99; suffix++ {
		candidate := InstanceName(nick, suffix)
		if _, clash := taken[strings.ToLower(candidate)]; !clash {
			return candidate
		}
	}
	// Extremely unlikely: 99 FlowDrop instances on one LAN. Stay deterministic
	// and within the label limit; a later restart can pick another suffix.
	return InstanceName(nick, 100)
}

func browseExisting(ctx context.Context, iface net.Interface) ([]string, error) {
	resolver, err := zeroconf.NewResolver(
		zeroconf.SelectIPTraffic(zeroconf.IPv4),
		zeroconf.SelectIfaces([]net.Interface{iface}),
	)
	if err != nil {
		return nil, err
	}
	browseCtx, cancel := context.WithTimeout(ctx, browseTimeout)
	defer cancel()

	entries := make(chan *zeroconf.ServiceEntry, 16)
	if err := resolver.Browse(browseCtx, ServiceType, Domain, entries); err != nil {
		return nil, err
	}
	var names []string
	seen := make(map[string]struct{})
	for entry := range entries {
		key := strings.ToLower(entry.Instance)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		names = append(names, entry.Instance)
	}
	return names, nil
}

func defaultRegister(instance string, t Target, host string, txt []string, iface net.Interface, log *slog.Logger) (Publisher, error) {
	return startResponder(instance, t, host, txt, iface, log)
}
