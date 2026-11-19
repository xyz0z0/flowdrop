package main

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"flowdrop/internal/config"
	"flowdrop/internal/netbind"
)

// collectLines runs reportAutoBind with a recorder instead of stdout, so doctor's
// auto_bind lines can be asserted without binding a port or opening a database.
func collectLines(t *testing.T, cfg config.Config) (lines []string, ok bool) {
	t.Helper()
	ok = true
	report := func(name string, good bool, detail string) {
		if !good {
			ok = false
		}
		lines = append(lines, name+" "+detail)
	}
	reportAutoBind(cfg, report)
	return lines, ok
}

// fakeScan replaces the address source for one test and restores it afterwards.
func fakeScan(t *testing.T, addrs []netbind.Addr, err error) {
	t.Helper()
	orig := netbind.Scan
	netbind.Scan = func() ([]netbind.Addr, error) { return addrs, err }
	t.Cleanup(func() { netbind.Scan = orig })
}

func TestDoctorAutoBindOff(t *testing.T) {
	c := config.Default()
	lines, ok := collectLines(t, c)
	if !ok {
		t.Fatalf("auto_bind=off must not warn: %v", lines)
	}
	if len(lines) != 1 || lines[0] != "auto_bind off" {
		t.Fatalf("lines = %v, want one \"auto_bind off\"", lines)
	}
}

func TestDoctorAutoBindEphemeralPort(t *testing.T) {
	fakeScan(t, nil, errors.New("must not be called"))
	c := config.Default()
	c.AutoBind = true
	c.Listen = "127.0.0.1:0"
	lines, ok := collectLines(t, c)
	if ok {
		t.Fatalf("port 0 must be reported as a problem: %v", lines)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "port 0") {
		t.Fatalf("lines = %v, want a single port-0 warning", lines)
	}
}

func TestDoctorAutoBindScanError(t *testing.T) {
	fakeScan(t, nil, errors.New("no interfaces"))
	c := config.Default()
	c.AutoBind = true
	c.Listen = "127.0.0.1:8080"
	lines, ok := collectLines(t, c)
	if ok || len(lines) != 1 || !strings.Contains(lines[0], "scan failed") {
		t.Fatalf("lines = %v ok = %v, want one scan-failed warning", lines, ok)
	}
}

// TestDoctorAutoBindListsTargets checks the happy path and the dedupe against
// explicit listeners, including the port spelling difference.
func TestDoctorAutoBindListsTargets(t *testing.T) {
	fakeScan(t, []netbind.Addr{
		{Iface: "lo", IP: netip.MustParseAddr("127.0.0.1"), Kind: netbind.KindLoopback},
		{Iface: "eth0", IP: netip.MustParseAddr("192.168.1.20"), Kind: netbind.KindLAN},
		{Iface: "eth0", IP: netip.MustParseAddr("10.0.0.7"), Kind: netbind.KindLAN},
		{Iface: "tailscale0", IP: netip.MustParseAddr("100.64.0.9"), Kind: netbind.KindTailscale},
	}, nil)
	c := config.Default()
	c.AutoBind = true
	c.Listen = "127.0.0.1:8080"
	// Already explicit: must not be printed again as an auto_bind target.
	c.AdditionalListen = []string{"10.0.0.7:08080"}
	lines, ok := collectLines(t, c)
	if !ok {
		t.Fatalf("an eligible address set must not warn: %v", lines)
	}
	want := []string{
		"auto_bind on",
		"auto_bind target 192.168.1.20:8080",
		"auto_bind target 100.64.0.9:8080",
	}
	if len(lines) != len(want) {
		t.Fatalf("lines = %v, want %v", lines, want)
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Fatalf("lines = %v, want %v", lines, want)
		}
	}
}

// TestDoctorAutoBindNoEligibleAddress is the common laptop-off-the-LAN case:
// the option is on but nothing qualifies yet, which is not a problem.
func TestDoctorAutoBindNoEligibleAddress(t *testing.T) {
	fakeScan(t, []netbind.Addr{
		{Iface: "lo", IP: netip.MustParseAddr("127.0.0.1"), Kind: netbind.KindLoopback},
	}, nil)
	c := config.Default()
	c.AutoBind = true
	c.Listen = "127.0.0.1:8080"
	lines, ok := collectLines(t, c)
	if !ok || len(lines) != 1 || !strings.Contains(lines[0], "no eligible") {
		t.Fatalf("lines = %v ok = %v, want a single non-warning line", lines, ok)
	}
}

func TestCanonicalAddr(t *testing.T) {
	pairs := [][2]string{
		{"10.0.0.1:08080", "10.0.0.1:8080"},
		{"[::1]:8080", "[::1]:8080"},
		{"0.0.0.0:8080", "0.0.0.0:8080"},
	}
	for _, p := range pairs {
		if got := canonicalAddr(p[0]); got != p[1] {
			t.Errorf("canonicalAddr(%q) = %q, want %q", p[0], got, p[1])
		}
	}
}
