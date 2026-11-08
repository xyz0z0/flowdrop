package main

import (
	"errors"
	"fmt"
	"net/url"

	"flowdrop/desktop/internal/network"
)

// AccessURL is one real, currently bound Hub URL as shown in the settings
// window. It is only ever built from the Hub's actual listener URLs, never
// from the configured address list, so the window cannot claim an unbound
// address is reachable.
type AccessURL struct {
	URL     string `json:"url"`
	Address string `json:"address"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Primary bool   `json:"primary"`
}

// describeAccessURLs labels the Hub's actual bound URLs with the interface kind
// the user picked. An address that is not in the current interface list is
// still reported (the listener really is bound) but labelled honestly.
func describeAccessURLs(list []network.Interface, urls []string) []AccessURL {
	out := make([]AccessURL, 0, len(urls))
	for i, raw := range urls {
		entry := AccessURL{URL: raw, Primary: i == 0, Kind: string(network.KindOther)}
		if u, err := url.Parse(raw); err == nil {
			entry.Address = u.Hostname()
		}
		iface, ok := network.Find(list, entry.Address)
		if ok {
			entry.Kind = string(iface.Kind)
			entry.Label = iface.Label
		} else {
			entry.Label = "本机地址（未在当前网卡列表中匹配到）"
		}
		out = append(out, entry)
	}
	return out
}

// preferredAccessURL picks the inbox window's URL: the first bound LAN address,
// otherwise the Hub's primary URL. It never invents a URL; an empty result means
// nothing is bound.
func preferredAccessURL(list []network.Interface, urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if iface, ok := network.Find(list, u.Hostname()); ok && iface.Kind == network.KindLAN {
			return raw
		}
	}
	return urls[0]
}

// validateDualSelection enforces the product's single dual mode: exactly two
// concrete local IPv4 addresses, a LAN address as the primary listener and a
// Tailscale address as the additional listener, both on the same port. A
// single-address selection (no additional addresses) is always left to the
// normal single-address validation. Missing Tailscale is reported as "not
// selectable", never as an instruction to install it.
func validateDualSelection(list []network.Interface, primary string, additional []string) error {
	if len(additional) == 0 {
		return nil
	}
	if len(additional) != 1 {
		return errors.New("「同时使用内网和 Tailscale」需要正好两个地址（一个内网、一个 Tailscale）")
	}
	if primary == additional[0] {
		return errors.New("两个地址不能相同")
	}
	primaryIface, ok := network.Find(list, primary)
	if !ok {
		return fmt.Errorf("本机当前没有 %s 这个 IPv4 地址，请重新选择", primary)
	}
	if primaryIface.Kind != network.KindLAN {
		return fmt.Errorf("%s 不是内网（LAN）地址；双模式的主地址必须选内网地址，Tailscale 地址放在第二个", primary)
	}
	extraIface, ok := network.Find(list, additional[0])
	if !ok {
		return fmt.Errorf("本机当前没有 %s 这个 IPv4 地址，请重新选择", additional[0])
	}
	if extraIface.Kind != network.KindTailscale {
		if !network.HasTailscale(list) {
			return errors.New("当前没有检测到 Tailscale 网卡，不能选择「同时使用内网和 Tailscale」。\n可以继续使用内网单地址模式；本应用不会要求或自动安装 Tailscale")
		}
		return fmt.Errorf("%s 不是 Tailscale 地址；双模式的第二个地址必须是 Tailscale 地址", additional[0])
	}
	if !network.HasLAN(list) {
		return errors.New("当前没有检测到内网（LAN）地址，不能选择双模式")
	}
	return nil
}

// dualModeAvailable reports whether the machine currently has both address
// kinds dual mode needs. Without Tailscale the UI disables dual mode instead of
// pretending it can bind one.
func dualModeAvailable(list []network.Interface) bool {
	return network.HasLAN(list) && network.HasTailscale(list)
}

// defaultDualPicks returns the addresses to preselect in dual mode.
func defaultDualPicks(list []network.Interface) (lan, tailscale string) {
	for _, i := range list {
		if i.Kind == network.KindLAN && lan == "" {
			lan = i.Address
		}
	}
	for _, i := range list {
		if i.Kind == network.KindTailscale && tailscale == "" {
			tailscale = i.Address
		}
	}
	return lan, tailscale
}
