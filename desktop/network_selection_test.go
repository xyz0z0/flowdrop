package main

import (
	"strings"
	"testing"

	"flowdrop/desktop/internal/network"
)

func testInterfaces() []network.Interface {
	return []network.Interface{
		{Name: "lo", Address: "127.0.0.1", Kind: network.KindLoopback, Label: "本机回环"},
		{Name: "eno1", Address: "192.168.1.10", Kind: network.KindLAN, Label: "局域网（LAN）· eno1"},
		{Name: "tailscale0", Address: "100.64.0.5", Kind: network.KindTailscale, Label: "Tailscale · tailscale0"},
		{Name: "docker0", Address: "172.17.0.1", Kind: network.KindVirtual, Label: "容器/虚拟网卡"},
	}
}

func TestDescribeAccessURLsUsesActualBoundURLsOnly(t *testing.T) {
	list := testInterfaces()
	got := describeAccessURLs(list, []string{
		"http://192.168.1.10:8080",
		"http://100.64.0.5:8080",
	})
	if len(got) != 2 {
		t.Fatalf("describeAccessURLs = %+v, want two entries", got)
	}
	if !got[0].Primary || got[0].Kind != string(network.KindLAN) || got[0].Address != "192.168.1.10" {
		t.Fatalf("primary entry = %+v", got[0])
	}
	if got[1].Primary || got[1].Kind != string(network.KindTailscale) {
		t.Fatalf("additional entry = %+v", got[1])
	}

	// A URL whose address is no longer in the interface list is still a real
	// listener, but must not borrow a LAN/Tailscale label it cannot prove.
	unknown := describeAccessURLs(list, []string{"http://192.0.2.9:8080"})
	if len(unknown) != 1 || unknown[0].Kind == string(network.KindLAN) || !strings.Contains(unknown[0].Label, "未在当前网卡列表") {
		t.Fatalf("unknown address description = %+v", unknown)
	}
	if empty := describeAccessURLs(list, nil); empty == nil || len(empty) != 0 {
		t.Fatalf("describeAccessURLs(nil) = %#v, want non-nil empty", empty)
	}
}

func TestPreferredAccessURLPrefersLAN(t *testing.T) {
	list := testInterfaces()
	// Even if the Hub reports the Tailscale URL first, the inbox prefers LAN.
	if got := preferredAccessURL(list, []string{"http://100.64.0.5:8080", "http://192.168.1.10:8080"}); got != "http://192.168.1.10:8080" {
		t.Fatalf("preferredAccessURL = %q, want the LAN URL", got)
	}
	// Without LAN, keep the Hub's primary URL instead of inventing one.
	if got := preferredAccessURL(list, []string{"http://100.64.0.5:8080"}); got != "http://100.64.0.5:8080" {
		t.Fatalf("preferredAccessURL(tailscale only) = %q", got)
	}
	if got := preferredAccessURL(list, nil); got != "" {
		t.Fatalf("preferredAccessURL(nil) = %q, want empty", got)
	}
}

func TestValidateDualSelection(t *testing.T) {
	list := testInterfaces()
	cases := []struct {
		name       string
		primary    string
		additional []string
		want       string
	}{
		{"single is untouched", "192.168.1.10", nil, ""},
		{"lan+tailscale", "192.168.1.10", []string{"100.64.0.5"}, ""},
		{"two extras", "192.168.1.10", []string{"100.64.0.5", "100.64.0.6"}, "正好两个"},
		{"duplicate", "192.168.1.10", []string{"192.168.1.10"}, "不能相同"},
		{"primary not LAN", "100.64.0.5", []string{"192.168.1.10"}, "必须选内网"},
		{"extra not tailscale", "192.168.1.10", []string{"172.17.0.1"}, "第二个地址必须是 Tailscale"},
		{"primary absent", "192.0.2.1", []string{"100.64.0.5"}, "本机当前没有"},
		{"extra absent", "192.168.1.10", []string{"100.64.0.99"}, "本机当前没有"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDualSelection(list, tc.primary, tc.additional)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("validateDualSelection = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateDualSelection = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestValidateDualSelectionWithoutTailscale(t *testing.T) {
	list := []network.Interface{
		{Name: "lo", Address: "127.0.0.1", Kind: network.KindLoopback},
		{Name: "eno1", Address: "192.168.1.10", Kind: network.KindLAN},
		{Name: "docker0", Address: "172.17.0.1", Kind: network.KindVirtual},
	}
	if dualModeAvailable(list) {
		t.Fatal("dualModeAvailable = true without a Tailscale address")
	}
	err := validateDualSelection(list, "192.168.1.10", []string{"172.17.0.1"})
	if err == nil || !strings.Contains(err.Error(), "没有检测到 Tailscale 网卡") {
		t.Fatalf("validateDualSelection = %v, want the no-Tailscale message", err)
	}
	if strings.Contains(err.Error(), "安装 Tailscale") && !strings.Contains(err.Error(), "不会要求或自动安装") {
		t.Fatalf("message must not tell the user to install Tailscale: %v", err)
	}
}

func TestDefaultDualPicks(t *testing.T) {
	lan, ts := defaultDualPicks(testInterfaces())
	if lan != "192.168.1.10" || ts != "100.64.0.5" {
		t.Fatalf("defaultDualPicks = %q,%q", lan, ts)
	}
}
