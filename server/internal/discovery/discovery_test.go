package discovery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/miekg/dns"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func testInterface(name string, index int, ip string) ifaceAddr {
	return ifaceAddr{
		Iface: net.Interface{
			Index: index,
			Name:  name,
			Flags: net.FlagUp | net.FlagMulticast,
		},
		IPs: []net.IP{net.ParseIP(ip).To4()},
	}
}

func TestEvaluateOnlyAdvertisesExplicitRFC1918PlainHTTP(t *testing.T) {
	lan := []ifaceAddr{testInterface("eno1", 2, "192.168.31.184")}

	t.Run("eligible LAN address", func(t *testing.T) {
		got, err := evaluate("192.168.31.184:8080", false, lan)
		if err != nil {
			t.Fatalf("eligible listen rejected: %v", err)
		}
		if !got.IP.Equal(net.ParseIP("192.168.31.184")) || got.Port != 8080 || got.Iface.Name != "eno1" {
			t.Fatalf("unexpected target: %+v", got)
		}
	})

	cases := []struct {
		name         string
		listen       string
		cookieSecure bool
		ifaces       []ifaceAddr
		wantSubstr   string
	}{
		{"loopback", "127.0.0.1:8080", false, lan, "loopback"},
		{"wildcard", "0.0.0.0:8080", false, lan, "wildcard"},
		{"empty host", ":8080", false, lan, "wildcard"},
		{"Tailscale CGNAT", "100.125.168.63:8080", false,
			[]ifaceAddr{testInterface("tailscale0", 4, "100.125.168.63")}, "Tailscale"},
		{"link local", "169.254.10.20:8080", false,
			[]ifaceAddr{testInterface("eno1", 2, "169.254.10.20")}, "link-local"},
		{"public", "203.0.113.5:8080", false,
			[]ifaceAddr{testInterface("eno1", 2, "203.0.113.5")}, "not RFC1918"},
		{"IPv6 LAN", "[fd00::5]:8080", false,
			[]ifaceAddr{testInterface("eno1", 2, "192.168.31.184")}, "not IPv4"},
		{"hostname not IP", "hub.local:8080", false, lan, "literal IP"},
		{"cookie secure", "192.168.31.184:8080", true, lan, "cookie_secure=true"},
		{"address not local", "192.168.9.9:8080", false, lan, "not assigned"},
		{"interface without multicast", "192.168.31.184:8080", false,
			[]ifaceAddr{{Iface: net.Interface{Index: 2, Name: "eno1", Flags: net.FlagUp},
				IPs: []net.IP{net.ParseIP("192.168.31.184").To4()}}}, "does not support multicast"},
		{"interface down", "192.168.31.184:8080", false,
			[]ifaceAddr{{Iface: net.Interface{Index: 2, Name: "eno1", Flags: net.FlagMulticast},
				IPs: []net.IP{net.ParseIP("192.168.31.184").To4()}}}, "is down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := evaluate(tc.listen, tc.cookieSecure, tc.ifaces)
			if err == nil {
				t.Fatalf("listen %q accepted; want rejection", tc.listen)
			}
			if !errors.Is(err, ErrIneligible) {
				t.Fatalf("error = %v; want ErrIneligible", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("error %q does not contain %q", err, tc.wantSubstr)
			}
		})
	}
}

func TestEvaluateAcceptsAllRFC1918Blocks(t *testing.T) {
	for _, ip := range []string{"10.1.2.3", "172.16.0.5", "172.31.255.254", "192.168.1.1"} {
		ifaces := []ifaceAddr{testInterface("lan0", 3, ip)}
		if _, err := evaluate(ip+":9000", false, ifaces); err != nil {
			t.Errorf("%s: rejected: %v", ip, err)
		}
	}
}

func TestSanitizeNickname(t *testing.T) {
	cases := map[string]string{
		"bob-hpelitedesk880g6":       "bob-hpelitedesk880g6",
		"bob-hpelitedesk880g6.local": "bob-hpelitedesk880g6",
		"Bob's PC (2)":               "Bob-s-PC-2",
		"  --weird..name--  ":        "weird",
		"":                           "Hub",
		"***":                        "Hub",
		"主机名":                        "Hub",
		"a--b":                       "a-b",
	}
	for in, want := range cases {
		if got := SanitizeNickname(in); got != want {
			t.Errorf("SanitizeNickname(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstanceNameTruncationAndConflicts(t *testing.T) {
	if got := InstanceName("short", 1); got != "FlowDrop-short" {
		t.Fatalf("base instance = %q", got)
	}
	longNick := strings.Repeat("x", 80)
	for suffix := 1; suffix <= 100; suffix++ {
		name := InstanceName(longNick, suffix)
		if len(name) > MaxInstanceLabel {
			t.Fatalf("suffix %d: instance label is %d bytes: %q", suffix, len(name), name)
		}
		if !strings.HasPrefix(name, "FlowDrop-") {
			t.Fatalf("instance %q lost FlowDrop- prefix", name)
		}
	}

	existing := []string{"flowdrop-Host", "FLOWDROP-HOST-2"}
	got := UniqueInstanceName("Host", existing)
	if got != "FlowDrop-Host-3" {
		t.Fatalf("conflict name = %q, want FlowDrop-Host-3", got)
	}
	if got := UniqueInstanceName("Host", nil); got != "FlowDrop-Host" {
		t.Fatalf("no-conflict name = %q", got)
	}
}

type fakePublisher struct {
	mu       sync.Mutex
	shutdown int
}

func (p *fakePublisher) Shutdown() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.shutdown++
}

func (p *fakePublisher) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.shutdown
}

func TestStartPublishesFrozenRecordAndWithdraws(t *testing.T) {
	pub := &fakePublisher{}
	var (
		gotInstance string
		gotTarget   Target
		gotHost     string
		gotTXT      []string
		gotIface    net.Interface
	)
	opts := Options{
		Enabled:      true,
		Listen:       "192.168.31.184:8080",
		CookieSecure: false,
		Logger:       testLogger(),
		Hostname:     "bob-hpelitedesk8800.local",
		ifaces:       []ifaceAddr{testInterface("eno1", 2, "192.168.31.184")},
		browse: func(context.Context, net.Interface) ([]string, error) {
			return []string{"FlowDrop-bob-hpelitedesk8800"}, nil
		},
		register: func(instance string, target Target, host string, txt []string, iface net.Interface, _ *slog.Logger) (Publisher, error) {
			gotInstance, gotTarget, gotHost, gotTXT, gotIface = instance, target, host, txt, iface
			return pub, nil
		},
	}

	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if gotInstance != "FlowDrop-bob-hpelitedesk8800-2" {
		t.Fatalf("instance = %q; conflict suffix not applied", gotInstance)
	}
	if gotHost != "flowdrop-bob-hpelitedesk8800.local." {
		t.Fatalf("SRV target host = %q", gotHost)
	}
	if !gotTarget.IP.Equal(net.ParseIP("192.168.31.184")) || gotTarget.Port != 8080 {
		t.Fatalf("target = %+v", gotTarget)
	}
	if gotIface.Name != "eno1" {
		t.Fatalf("registered interface = %q", gotIface.Name)
	}
	wantTXT := []string{"v=1", "scheme=http", "path=/"}
	if len(gotTXT) != len(wantTXT) {
		t.Fatalf("TXT = %v", gotTXT)
	}
	for i := range wantTXT {
		if gotTXT[i] != wantTXT[i] {
			t.Fatalf("TXT[%d] = %q, want %q", i, gotTXT[i], wantTXT[i])
		}
		if strings.Contains(strings.ToLower(gotTXT[i]), "password") || strings.Contains(gotTXT[i], "/home/") {
			t.Fatalf("TXT leaks sensitive data: %q", gotTXT[i])
		}
	}

	h.Close()
	h.Close() // idempotent
	if got := pub.calls(); got != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", got)
	}
}

func TestStartDoesNotPublishWhenDisabledOrIneligible(t *testing.T) {
	registered := 0
	register := func(string, Target, string, []string, net.Interface, *slog.Logger) (Publisher, error) {
		registered++
		return &fakePublisher{}, nil
	}
	base := Options{
		Enabled:      true,
		Listen:       "192.168.31.184:8080",
		CookieSecure: false,
		Logger:       testLogger(),
		Hostname:     "host",
		ifaces:       []ifaceAddr{testInterface("eno1", 2, "192.168.31.184")},
		register:     register,
	}

	disabled := base
	disabled.Enabled = false
	if h, err := Start(context.Background(), disabled); !errors.Is(err, ErrDisabled) || h != nil {
		t.Fatalf("disabled Start = (%v, %v); want ErrDisabled", h, err)
	}

	for _, mutate := range []func(*Options){
		func(o *Options) { o.Listen = "127.0.0.1:8080" },
		func(o *Options) { o.Listen = "100.125.168.63:8080" },
		func(o *Options) { o.CookieSecure = true },
	} {
		opts := base
		mutate(&opts)
		if h, err := Start(context.Background(), opts); !errors.Is(err, ErrIneligible) || h != nil {
			t.Fatalf("ineligible Start(%+v) = (%v, %v); want ErrIneligible", opts.Listen, h, err)
		}
	}
	if registered != 0 {
		t.Fatalf("register called %d times for disabled/ineligible configs", registered)
	}
}

func TestStartKeepsPublishingWhenConflictScanFails(t *testing.T) {
	gotInstance := ""
	opts := Options{
		Enabled:      true,
		Listen:       "192.168.31.184:8080",
		CookieSecure: false,
		Logger:       testLogger(),
		Hostname:     "host",
		ifaces:       []ifaceAddr{testInterface("eno1", 2, "192.168.31.184")},
		browse: func(context.Context, net.Interface) ([]string, error) {
			return nil, errors.New("5353 busy")
		},
		register: func(instance string, _ Target, _ string, _ []string, _ net.Interface, _ *slog.Logger) (Publisher, error) {
			gotInstance = instance
			return &fakePublisher{}, nil
		},
	}
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start with failed browse: %v", err)
	}
	defer h.Close()
	if gotInstance != "FlowDrop-host" {
		t.Fatalf("instance = %q, want base name", gotInstance)
	}
}

func TestStartReturnsPublishError(t *testing.T) {
	wantErr := errors.New("bind udp 5353: address already in use")
	opts := Options{
		Enabled:      true,
		Listen:       "192.168.31.184:8080",
		CookieSecure: false,
		Logger:       testLogger(),
		Hostname:     "host",
		ifaces:       []ifaceAddr{testInterface("eno1", 2, "192.168.31.184")},
		browse:       func(context.Context, net.Interface) ([]string, error) { return nil, nil },
		register: func(string, Target, string, []string, net.Interface, *slog.Logger) (Publisher, error) {
			return nil, wantErr
		},
	}
	h, err := Start(context.Background(), opts)
	if err == nil || h != nil {
		t.Fatalf("Start = (%v, %v); want publish error", h, err)
	}
	if !errors.Is(err, wantErr) && !strings.Contains(err.Error(), wantErr.Error()) {
		t.Fatalf("error %v does not wrap %v", err, wantErr)
	}
}

func TestFrozenProtocolConstants(t *testing.T) {
	if ServiceTypeFQDN != "_flowdrop._tcp.local." {
		t.Fatalf("service type = %q", ServiceTypeFQDN)
	}
	if got := fmt.Sprint(TXT()); got != "[v=1 scheme=http path=/]" {
		t.Fatalf("TXT = %s", got)
	}
}

func TestResponderRecordsMatchFrozenProtocol(t *testing.T) {
	r := &responder{
		ip:           net.ParseIP("192.168.31.184").To4(),
		port:         4321,
		instanceName: dns.Fqdn("FlowDrop-host._flowdrop._tcp.local."),
		hostName:     dns.Fqdn("flowdrop-host.local."),
		serviceName:  ServiceTypeFQDN,
		txt:          TXT(),
		ttl:          120,
	}
	r.buildRecords()

	ptr, ok := r.ptr.(*dns.PTR)
	if !ok || ptr.Ptr != r.instanceName || ptr.Hdr.Name != ServiceTypeFQDN || ptr.Hdr.Ttl != 120 {
		t.Fatalf("PTR = %#v", r.ptr)
	}
	srv, ok := r.srv.(*dns.SRV)
	if !ok || srv.Port != 4321 || srv.Target != r.hostName || srv.Hdr.Ttl != 120 {
		t.Fatalf("SRV = %#v", r.srv)
	}
	if srv.Hdr.Class&0x8000 == 0 {
		t.Fatal("SRV must set the mDNS cache-flush bit")
	}
	txt, ok := r.txtRR.(*dns.TXT)
	if !ok || strings.Join(txt.Txt, ",") != "v=1,scheme=http,path=/" || txt.Hdr.Ttl != 120 {
		t.Fatalf("TXT = %#v", r.txtRR)
	}
	a, ok := r.a.(*dns.A)
	if !ok || !a.A.Equal(net.ParseIP("192.168.31.184")) || a.Hdr.Name != r.hostName || a.Hdr.Ttl != 120 {
		t.Fatalf("A = %#v", r.a)
	}
	if a.Hdr.Class&0x8000 == 0 {
		t.Fatal("A must set the mDNS cache-flush bit")
	}
}

func TestStartPublishesEveryEligibleLanListenerAndSkipsTailscale(t *testing.T) {
	ifaces := []ifaceAddr{
		testInterface("eno1", 2, "192.168.31.184"),
		testInterface("wlan0", 3, "192.168.1.20"),
		testInterface("tailscale0", 4, "100.125.168.63"),
		// Present on the host but deliberately not bound: it must not be
		// advertised just because it exists.
		testInterface("eth9", 5, "10.99.0.9"),
	}
	type published struct {
		instance string
		target   Target
		host     string
	}
	var got []published
	var pubs []*fakePublisher
	opts := Options{
		Enabled: true,
		// Loopback primary is never advertised, but it must not suppress the
		// eligible LAN additional listeners.
		Listen:           "127.0.0.1:8080",
		AdditionalListen: []string{"192.168.31.184:8081", "100.125.168.63:8081", "192.168.1.20:8082"},
		CookieSecure:     false,
		Logger:           testLogger(),
		Hostname:         "host",
		ifaces:           ifaces,
		browse:           func(context.Context, net.Interface) ([]string, error) { return nil, nil },
		register: func(instance string, target Target, host string, _ []string, _ net.Interface, _ *slog.Logger) (Publisher, error) {
			p := &fakePublisher{}
			pubs = append(pubs, p)
			got = append(got, published{instance: instance, target: target, host: host})
			return p, nil
		},
	}
	h, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("published %d records (%v), want 2 LAN records", len(got), got)
	}
	if !got[0].target.IP.Equal(net.ParseIP("192.168.31.184")) || got[0].target.Port != 8081 || got[0].target.Iface.Name != "eno1" {
		t.Fatalf("first target = %+v", got[0].target)
	}
	if !got[1].target.IP.Equal(net.ParseIP("192.168.1.20")) || got[1].target.Port != 8082 || got[1].target.Iface.Name != "wlan0" {
		t.Fatalf("second target = %+v", got[1].target)
	}
	for _, rec := range got {
		if rec.target.IP.Equal(net.ParseIP("100.125.168.63")) {
			t.Fatalf("Tailscale/CGNAT address was advertised: %+v", rec)
		}
		if rec.target.IP.Equal(net.ParseIP("10.99.0.9")) {
			t.Fatalf("unbound interface address was advertised: %+v", rec)
		}
		if rec.instance == "" || rec.host == "" {
			t.Fatalf("incomplete record: %+v", rec)
		}
	}
	if got[0].instance == got[1].instance {
		t.Fatalf("duplicate instance name %q", got[0].instance)
	}
	if got[0].host == got[1].host {
		t.Fatalf("duplicate SRV target host %q", got[0].host)
	}

	h.Close()
	if pubs[0].calls() != 1 || pubs[1].calls() != 1 {
		t.Fatalf("withdraw calls = %d/%d, want 1/1", pubs[0].calls(), pubs[1].calls())
	}
}

func TestStartWithdrawsEarlierPublishWhenLaterRegistrationFails(t *testing.T) {
	first := &fakePublisher{}
	calls := 0
	opts := Options{
		Enabled:          true,
		Listen:           "192.168.31.184:8080",
		AdditionalListen: []string{"192.168.1.20:8081"},
		CookieSecure:     false,
		Logger:           testLogger(),
		Hostname:         "host",
		ifaces: []ifaceAddr{
			testInterface("eno1", 2, "192.168.31.184"),
			testInterface("wlan0", 3, "192.168.1.20"),
		},
		browse: func(context.Context, net.Interface) ([]string, error) { return nil, nil },
		register: func(string, Target, string, []string, net.Interface, *slog.Logger) (Publisher, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			return nil, errors.New("udp 5353 unavailable")
		},
	}
	h, err := Start(context.Background(), opts)
	if err == nil || h != nil {
		t.Fatalf("Start = (%v, %v); want publish error", h, err)
	}
	if first.calls() != 1 {
		t.Fatalf("first record not withdrawn after later failure: %d", first.calls())
	}
}
