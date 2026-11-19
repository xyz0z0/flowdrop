// Package netbind selects the local IPv4 addresses a Hub should bind
// automatically, and diffs successive scans so the Hub can add and drop
// listeners without a restart.
package netbind

import (
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type Kind string

const (
	KindLAN       Kind = "lan"
	KindTailscale Kind = "tailscale"
	KindVirtual   Kind = "virtual"
	KindOther     Kind = "other"
	KindLoopback  Kind = "loopback"
)

// Addr is one IPv4 address of one interface.
type Addr struct {
	Iface string
	IP    netip.Addr
	Kind  Kind
}

var (
	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")
	lanPrefixes = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
	}
)

// virtualPrefixes are interface name prefixes that mark container/VM/tunnel
// bridges. Matching is prefix-based on purpose: substring matching would
// misclassify real NICs. "br" covers both "br0" and "br-<id>".
var virtualPrefixes = []string{
	"docker",
	"veth",
	"br",
	"virbr",
	"lxc",
	"lxcbr",
	"podman",
	"cni",
	"flannel",
	"vmnet",
	"vboxnet",
	"tun",
	"tap",
	"wg",
}

// Scan lists this machine's IPv4 addresses. It is a variable so tests and the
// Hub can substitute a deterministic source.
var Scan = func() ([]Addr, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list interfaces: %w", err)
	}
	var addrs []Addr
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range ifaceAddrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil {
				continue
			}
			if ip4.IsLinkLocalUnicast() {
				continue
			}
			ip, ok := netip.AddrFromSlice(ip4)
			if !ok {
				continue
			}
			ip = ip.Unmap()
			addrs = append(addrs, Addr{Iface: iface.Name, IP: ip, Kind: Classify(iface.Name, ip)})
		}
	}
	return addrs, nil
}

// Classify labels one interface IPv4 address.
func Classify(ifaceName string, ip netip.Addr) Kind {
	if ip.IsLoopback() {
		return KindLoopback
	}
	if strings.HasPrefix(ifaceName, "tailscale") || cgnatPrefix.Contains(ip) {
		return KindTailscale
	}
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(ifaceName, prefix) {
			return KindVirtual
		}
	}
	if isPrivateIPv4(ip) {
		return KindLAN
	}
	return KindOther
}

// isPrivateIPv4 reports whether ip is in an RFC1918 range
// (10/8, 172.16/12, 192.168/16).
func isPrivateIPv4(ip netip.Addr) bool {
	for _, prefix := range lanPrefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// Targets returns "host:port" for every address that should be auto-bound:
// every private IPv4 of this machine, i.e. RFC1918 LAN addresses, Tailscale
// CGNAT (100.64/10) and container/VM bridge addresses whose IP is RFC1918 too
// (docker0, br-*, virbr*). Bridges are bound on purpose for LocalSend parity so
// containers on this machine can reach the Hub; they are not advertised over
// mDNS (see the Hub's filterAdvertised). Loopback, link-local and public
// addresses are never bound.
// Ordered LAN (IP ascending), Tailscale (IP ascending), Virtual (IP ascending),
// deduplicated by IP.
func Targets(addrs []Addr, port string) []string {
	if !validPort(port) {
		return nil
	}
	seen := make(map[netip.Addr]struct{}, len(addrs))
	picked := make([]Addr, 0, len(addrs))
	for _, a := range addrs {
		if !binds(a) {
			continue
		}
		if _, dup := seen[a.IP]; dup {
			continue
		}
		seen[a.IP] = struct{}{}
		picked = append(picked, a)
	}
	if len(picked) == 0 {
		return nil
	}
	sort.SliceStable(picked, func(i, j int) bool {
		ri, rj := kindRank(picked[i].Kind), kindRank(picked[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return picked[i].IP.Compare(picked[j].IP) < 0
	})
	out := make([]string, 0, len(picked))
	for _, a := range picked {
		out = append(out, net.JoinHostPort(a.IP.String(), port))
	}
	return out
}

// Diff returns the addresses added and removed between two target sets.
// Both results are sorted; order of the inputs does not matter.
func Diff(oldTargets, newTargets []string) (added, removed []string) {
	oldSet := make(map[string]struct{}, len(oldTargets))
	for _, t := range oldTargets {
		oldSet[t] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(newTargets))
	for _, t := range newTargets {
		newSet[t] = struct{}{}
	}
	for t := range newSet {
		if _, ok := oldSet[t]; !ok {
			added = append(added, t)
		}
	}
	for t := range oldSet {
		if _, ok := newSet[t]; !ok {
			removed = append(removed, t)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

// binds reports whether one scanned address is eligible for auto binding: a
// private IPv4, on a real NIC or on a container/VM bridge. A bridge with a
// public address stays unbound, and so does every KindOther address.
func binds(a Addr) bool {
	switch a.Kind {
	case KindLAN, KindTailscale:
		return true
	case KindVirtual:
		return isPrivateIPv4(a.IP)
	default:
		return false
	}
}

func kindRank(k Kind) int {
	switch k {
	case KindLAN:
		return 0
	case KindTailscale:
		return 1
	default:
		return 2
	}
}

// validPort reports whether port can be reused for every auto-bound listener.
func validPort(port string) bool {
	if port == "" || port == "0" {
		return false
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return false
	}
	return true
}
