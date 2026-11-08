// Package network enumerates the machine's real IPv4 addresses and labels them
// so the settings window can offer an honest choice between a home LAN address
// and a Tailscale address. It never invents an address: if nothing suitable is
// present, the caller gets loopback and a reason to show the user.
package network

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Kind classifies an address by how a phone would reach it.
type Kind string

const (
	KindLAN       Kind = "lan"
	KindTailscale Kind = "tailscale"
	KindVirtual   Kind = "virtual"
	KindOther     Kind = "other"
	KindLoopback  Kind = "loopback"
)

// Interface is one IPv4 address on one interface.
type Interface struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	Kind    Kind   `json:"kind"`
	// Recommended marks the address the desktop should preselect.
	Recommended bool `json:"recommended"`
	// Label is a short human description for the settings window.
	Label string `json:"label"`
}

// List returns every usable IPv4 address on this machine, best candidate first.
// Loopback is included last so the user can still choose it deliberately.
func List() ([]Interface, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var out []Interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}
			if ip.IsLinkLocalUnicast() {
				continue
			}
			kind := Classify(iface.Name, ip)
			out = append(out, Interface{
				Name:    iface.Name,
				Address: ip.String(),
				Kind:    kind,
				Label:   label(iface.Name, kind),
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return rank(out[i].Kind) < rank(out[j].Kind)
	})
	markRecommended(out)
	return out, nil
}

// Classify labels one address. Interface-name heuristics are used only to
// recognise Tailscale and container/bridge devices; the address ranges remain
// authoritative for LAN vs other.
func Classify(ifaceName string, ip net.IP) Kind {
	ip4 := ip.To4()
	if ip4 == nil {
		return KindOther
	}
	if ip4.IsLoopback() {
		return KindLoopback
	}
	name := strings.ToLower(ifaceName)
	if strings.HasPrefix(name, "tailscale") || isTailscaleCGNAT(ip4) {
		return KindTailscale
	}
	if isVirtualName(name) {
		return KindVirtual
	}
	if IsLAN(ip4) {
		return KindLAN
	}
	return KindOther
}

// IsLAN reports the RFC1918 home/office LAN ranges.
func IsLAN(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 10:
		return true
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return true
	case ip4[0] == 192 && ip4[1] == 168:
		return true
	}
	return false
}

// isTailscaleCGNAT matches the 100.64.0.0/10 range Tailscale hands out.
func isTailscaleCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127
}

func isVirtualName(name string) bool {
	prefixes := []string{"docker", "br-", "veth", "virbr", "vmnet", "lxc", "lxd", "podman", "cni", "flannel", "kube"}
	for _, p := range prefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func rank(k Kind) int {
	switch k {
	case KindLAN:
		return 0
	case KindTailscale:
		return 1
	case KindOther:
		return 2
	case KindVirtual:
		return 3
	default:
		return 4
	}
}

func label(ifaceName string, k Kind) string {
	switch k {
	case KindLAN:
		return "局域网（LAN）· " + ifaceName
	case KindTailscale:
		return "Tailscale · " + ifaceName
	case KindVirtual:
		return "容器/虚拟网卡（手机通常无法访问）· " + ifaceName
	case KindLoopback:
		return "本机回环（手机无法访问）"
	default:
		return "其他网卡 · " + ifaceName
	}
}

// markRecommended flags the first LAN address, otherwise the first Tailscale
// address, otherwise the first other address. Virtual devices and loopback are
// never auto-selected: phones usually cannot reach them.
func markRecommended(list []Interface) {
	for i := range list {
		if list[i].Kind == KindLAN {
			list[i].Recommended = true
			return
		}
	}
	for i := range list {
		if list[i].Kind == KindTailscale {
			list[i].Recommended = true
			return
		}
	}
	for i := range list {
		if list[i].Kind == KindOther {
			list[i].Recommended = true
			return
		}
	}
}

// DefaultBind picks the best address to preselect: a home-LAN address first, then
// a Tailscale address, then any other real address. Virtual/container devices
// and loopback are never auto-selected because phones cannot reach them.
// It returns the address and whether it is reachable from another device
// (false means loopback-only).
func DefaultBind(list []Interface) (string, bool) {
	for _, want := range []Kind{KindLAN, KindTailscale, KindOther} {
		for _, i := range list {
			if i.Kind == want {
				return i.Address, true
			}
		}
	}
	return "127.0.0.1", false
}

// HasTailscale reports whether any Tailscale address is present, so the UI can
// show an honest hint when cross-network access is unavailable.
func HasTailscale(list []Interface) bool { return hasKind(list, KindTailscale) }

// HasLAN reports whether any RFC1918 LAN address is present. Dual mode needs
// both a LAN and a Tailscale address.
func HasLAN(list []Interface) bool { return hasKind(list, KindLAN) }

func hasKind(list []Interface, kind Kind) bool {
	for _, i := range list {
		if i.Kind == kind {
			return true
		}
	}
	return false
}

// Find returns the entry for addr, if it currently exists on this machine.
func Find(list []Interface, addr string) (Interface, bool) {
	for _, i := range list {
		if i.Address == addr {
			return i, true
		}
	}
	return Interface{}, false
}
