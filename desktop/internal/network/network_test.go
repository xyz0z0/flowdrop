package network

import (
	"net"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		iface string
		ip    string
		want  Kind
	}{
		{"eno1", "192.168.31.184", KindLAN},
		{"wlp2s0", "10.0.5.7", KindLAN},
		{"eth0", "172.20.3.4", KindLAN},
		{"tailscale0", "100.125.168.63", KindTailscale},
		{"enp0s3", "100.100.100.100", KindTailscale}, // CGNAT range wins on any iface
		{"docker0", "172.17.0.1", KindVirtual},
		{"br-78cb69da0b47", "172.18.0.1", KindVirtual},
		{"lo", "127.0.0.1", KindLoopback},
		{"eno1", "8.8.8.8", KindOther},
		{"tailscale0", "192.168.1.10", KindTailscale}, // iface name wins for tailscale
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test IP %q", tc.ip)
		}
		if got := Classify(tc.iface, ip); got != tc.want {
			t.Errorf("Classify(%q, %q) = %q, want %q", tc.iface, tc.ip, got, tc.want)
		}
	}
}

func TestClassifyIgnoresIPv6(t *testing.T) {
	if got := Classify("eno1", net.ParseIP("fd7a:115c:a1e0::1")); got != KindOther {
		t.Fatalf("Classify(ipv6) = %q, want other", got)
	}
}

func TestIsLAN(t *testing.T) {
	lan := []string{"10.0.0.1", "10.255.255.254", "172.16.0.1", "172.31.255.254", "192.168.0.1", "192.168.255.254"}
	notLAN := []string{"172.15.0.1", "172.32.0.1", "192.169.0.1", "8.8.8.8", "100.64.0.1", "127.0.0.1"}
	for _, s := range lan {
		if !IsLAN(net.ParseIP(s)) {
			t.Errorf("IsLAN(%q) = false, want true", s)
		}
	}
	for _, s := range notLAN {
		if IsLAN(net.ParseIP(s)) {
			t.Errorf("IsLAN(%q) = true, want false", s)
		}
	}
}

func TestDefaultBindPrefersLANThenTailscaleThenOther(t *testing.T) {
	lanList := []Interface{
		{Name: "lo", Address: "127.0.0.1", Kind: KindLoopback},
		{Name: "docker0", Address: "172.17.0.1", Kind: KindVirtual},
		{Name: "tailscale0", Address: "100.64.0.5", Kind: KindTailscale},
		{Name: "eno1", Address: "192.168.1.9", Kind: KindLAN},
	}
	got, reachable := DefaultBind(lanList)
	if got != "192.168.1.9" || !reachable {
		t.Fatalf("DefaultBind(lan) = %q,%v want 192.168.1.9,true", got, reachable)
	}

	tailscaleOnly := []Interface{
		{Name: "lo", Address: "127.0.0.1", Kind: KindLoopback},
		{Name: "tailscale0", Address: "100.64.0.5", Kind: KindTailscale},
	}
	got, reachable = DefaultBind(tailscaleOnly)
	if got != "100.64.0.5" || !reachable {
		t.Fatalf("DefaultBind(tailscale) = %q,%v want 100.64.0.5,true", got, reachable)
	}

	none := []Interface{
		{Name: "lo", Address: "127.0.0.1", Kind: KindLoopback},
		{Name: "docker0", Address: "172.17.0.1", Kind: KindVirtual},
	}
	got, reachable = DefaultBind(none)
	if got != "127.0.0.1" || reachable {
		t.Fatalf("DefaultBind(none) = %q,%v want 127.0.0.1,false", got, reachable)
	}
}

func TestListContainsRealAddressesOnly(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, i := range list {
		ip := net.ParseIP(i.Address)
		if ip == nil || ip.To4() == nil {
			t.Fatalf("List returned non-IPv4 %q", i.Address)
		}
		if ip.IsLinkLocalUnicast() {
			t.Fatalf("List returned a link-local address %q", i.Address)
		}
		if i.Kind == "" || i.Label == "" {
			t.Fatalf("List entry %+v is missing kind/label", i)
		}
	}
}

func TestHasTailscaleAndFind(t *testing.T) {
	list := []Interface{{Name: "tailscale0", Address: "100.64.0.5", Kind: KindTailscale}}
	if !HasTailscale(list) {
		t.Fatal("HasTailscale = false, want true")
	}
	if _, ok := Find(list, "100.64.0.5"); !ok {
		t.Fatal("Find did not locate the tailscale address")
	}
	if HasTailscale([]Interface{{Address: "192.168.1.2", Kind: KindLAN}}) {
		t.Fatal("HasTailscale = true without a tailscale entry")
	}
}
