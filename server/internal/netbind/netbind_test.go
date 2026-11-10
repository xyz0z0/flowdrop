package netbind

import (
	"net/netip"
	"reflect"
	"testing"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	ip, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse addr %q: %v", s, err)
	}
	return ip
}

func TestClassify(t *testing.T) {
	cases := []struct {
		iface string
		ip    string
		kind  Kind
	}{
		{"docker0", "172.17.0.1", KindVirtual},
		{"br-ab12cd", "172.18.0.1", KindVirtual},
		{"br0", "10.20.0.1", KindVirtual},
		{"virbr0", "192.168.122.1", KindVirtual},
		{"veth1234", "10.0.1.5", KindVirtual},
		{"tailscale0", "100.64.1.2", KindTailscale},
		{"wg0", "100.64.3.4", KindTailscale},
		{"eth0", "192.168.1.20", KindLAN},
		{"wlan0", "10.0.0.7", KindLAN},
		{"eth1", "172.16.5.5", KindLAN},
		{"eth1", "172.32.0.1", KindOther},
		{"eth0", "8.8.8.8", KindOther},
		{"eth0", "169.254.3.3", KindOther},
		{"lo", "127.0.0.1", KindLoopback},
	}
	for _, tc := range cases {
		t.Run(tc.iface+"/"+tc.ip, func(t *testing.T) {
			if got := Classify(tc.iface, mustAddr(t, tc.ip)); got != tc.kind {
				t.Fatalf("Classify(%q, %s) = %q, want %q", tc.iface, tc.ip, got, tc.kind)
			}
		})
	}
}

func TestTargets(t *testing.T) {
	cases := []struct {
		name  string
		addrs []Addr
		port  string
		want  []string
	}{
		{
			name: "mixed filtered, deduped and ordered",
			addrs: []Addr{
				{Iface: "tailscale0", IP: mustAddr(t, "100.64.1.2"), Kind: KindTailscale},
				{Iface: "docker0", IP: mustAddr(t, "172.17.0.1"), Kind: KindVirtual},
				{Iface: "wlan0", IP: mustAddr(t, "10.0.0.7"), Kind: KindLAN},
				{Iface: "eth0", IP: mustAddr(t, "8.8.8.8"), Kind: KindOther},
				{Iface: "lo", IP: mustAddr(t, "127.0.0.1"), Kind: KindLoopback},
				{Iface: "eth2", IP: mustAddr(t, "10.0.0.7"), Kind: KindLAN},
				{Iface: "eth1", IP: mustAddr(t, "192.168.1.20"), Kind: KindLAN},
				{Iface: "tailscale1", IP: mustAddr(t, "100.64.1.1"), Kind: KindTailscale},
			},
			port: "47820",
			want: []string{
				"10.0.0.7:47820",
				"192.168.1.20:47820",
				"100.64.1.1:47820",
				"100.64.1.2:47820",
				"172.17.0.1:47820",
			},
		},
		{
			name: "container bridges with private addresses are bound",
			addrs: []Addr{
				{Iface: "br-78cb69da0b47", IP: mustAddr(t, "172.18.0.1"), Kind: KindVirtual},
				{Iface: "docker0", IP: mustAddr(t, "172.17.0.1"), Kind: KindVirtual},
				{Iface: "eno1", IP: mustAddr(t, "192.168.31.184"), Kind: KindLAN},
				{Iface: "tailscale0", IP: mustAddr(t, "100.125.168.63"), Kind: KindTailscale},
			},
			port: "8080",
			want: []string{
				"192.168.31.184:8080",
				"100.125.168.63:8080",
				"172.17.0.1:8080",
				"172.18.0.1:8080",
			},
		},
		{
			name: "public and link-local addresses are never bound",
			addrs: []Addr{
				{Iface: "eth0", IP: mustAddr(t, "203.0.113.9"), Kind: KindOther},
				{Iface: "eth0", IP: mustAddr(t, "169.254.3.3"), Kind: KindOther},
				{Iface: "lo", IP: mustAddr(t, "127.0.0.1"), Kind: KindLoopback},
				{Iface: "virbr0", IP: mustAddr(t, "8.8.4.4"), Kind: KindVirtual},
			},
			port: "8080",
			want: nil,
		},
		{
			name: "no eligible addresses returns nil",
			addrs: []Addr{
				{Iface: "eth0", IP: mustAddr(t, "203.0.113.9"), Kind: KindOther},
				{Iface: "lo", IP: mustAddr(t, "127.0.0.1"), Kind: KindLoopback},
			},
			port: "47820",
			want: nil,
		},
		{
			name:  "empty input returns nil",
			addrs: nil,
			port:  "47820",
			want:  nil,
		},
		{
			name:  "empty port returns nil",
			addrs: []Addr{{Iface: "eth0", IP: mustAddr(t, "192.168.1.20"), Kind: KindLAN}},
			port:  "",
			want:  nil,
		},
		{
			name:  "port zero returns nil",
			addrs: []Addr{{Iface: "eth0", IP: mustAddr(t, "192.168.1.20"), Kind: KindLAN}},
			port:  "0",
			want:  nil,
		},
		{
			name:  "invalid port returns nil",
			addrs: []Addr{{Iface: "eth0", IP: mustAddr(t, "192.168.1.20"), Kind: KindLAN}},
			port:  "70000",
			want:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Targets(tc.addrs, tc.port)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Targets() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestDiff(t *testing.T) {
	cases := []struct {
		name        string
		oldTargets  []string
		newTargets  []string
		wantAdded   []string
		wantRemoved []string
	}{
		{
			name:        "only added",
			oldTargets:  nil,
			newTargets:  []string{"10.0.0.7:1"},
			wantAdded:   []string{"10.0.0.7:1"},
			wantRemoved: nil,
		},
		{
			name:        "only removed",
			oldTargets:  []string{"10.0.0.7:1"},
			newTargets:  nil,
			wantAdded:   nil,
			wantRemoved: []string{"10.0.0.7:1"},
		},
		{
			name:        "both changed",
			oldTargets:  []string{"10.0.0.7:1", "192.168.1.20:1"},
			newTargets:  []string{"192.168.1.20:1", "100.64.1.2:1"},
			wantAdded:   []string{"100.64.1.2:1"},
			wantRemoved: []string{"10.0.0.7:1"},
		},
		{
			name:        "unchanged",
			oldTargets:  []string{"10.0.0.7:1", "192.168.1.20:1"},
			newTargets:  []string{"192.168.1.20:1", "10.0.0.7:1"},
			wantAdded:   nil,
			wantRemoved: nil,
		},
		{
			name:        "input order irrelevant",
			oldTargets:  []string{"192.168.1.20:1", "10.0.0.7:1", "100.64.1.1:1"},
			newTargets:  []string{"100.64.9.9:1", "10.0.0.8:1", "100.64.1.1:1"},
			wantAdded:   []string{"10.0.0.8:1", "100.64.9.9:1"},
			wantRemoved: []string{"10.0.0.7:1", "192.168.1.20:1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			added, removed := Diff(tc.oldTargets, tc.newTargets)
			if len(added) != len(tc.wantAdded) || len(removed) != len(tc.wantRemoved) {
				t.Fatalf("Diff() = (%#v, %#v), want (%#v, %#v)", added, removed, tc.wantAdded, tc.wantRemoved)
			}
			if !reflect.DeepEqual(added, tc.wantAdded) {
				t.Fatalf("added = %#v, want %#v", added, tc.wantAdded)
			}
			if !reflect.DeepEqual(removed, tc.wantRemoved) {
				t.Fatalf("removed = %#v, want %#v", removed, tc.wantRemoved)
			}
		})
	}
}

func TestScan(t *testing.T) {
	addrs, err := Scan()
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	sawLoopback := false
	for _, a := range addrs {
		if !a.IP.Is4() {
			t.Fatalf("Scan() returned non-IPv4 address %s on %s", a.IP, a.Iface)
		}
		if a.IP.IsLinkLocalUnicast() {
			t.Fatalf("Scan() returned link-local address %s on %s", a.IP, a.Iface)
		}
		if a.IP.IsLoopback() {
			sawLoopback = true
			if a.Kind != KindLoopback {
				t.Fatalf("loopback %s classified as %q", a.IP, a.Kind)
			}
		}
	}
	if !sawLoopback {
		t.Fatalf("Scan() did not include 127.0.0.1; got %#v", addrs)
	}
}

func TestScanIsReplaceable(t *testing.T) {
	orig := Scan
	t.Cleanup(func() { Scan = orig })

	Scan = func() ([]Addr, error) {
		return []Addr{
			{Iface: "eth0", IP: mustAddr(t, "192.168.1.20"), Kind: KindLAN},
			{Iface: "docker0", IP: mustAddr(t, "172.17.0.1"), Kind: KindVirtual},
			{Iface: "tailscale0", IP: mustAddr(t, "100.64.1.2"), Kind: KindTailscale},
		}, nil
	}

	addrs, err := Scan()
	if err != nil {
		t.Fatalf("stub Scan() error: %v", err)
	}
	got := Targets(addrs, "47820")
	want := []string{"192.168.1.20:47820", "100.64.1.2:47820", "172.17.0.1:47820"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Targets(Scan()) = %#v, want %#v", got, want)
	}
}
