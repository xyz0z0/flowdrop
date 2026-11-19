package hub_test

// Auto-binding coverage: the Hub binds every eligible local IPv4 on the primary
// port and keeps that set current while running, without a restart. 127.0.0.2
// and 127.0.0.3 stand in for LAN addresses (bindable on Linux without touching
// host networking); mDNS is disabled because multicast is often unavailable in
// test environments. Real NIC add/remove and a real mDNS re-registration remain
// on-hardware verification.

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"flowdrop/internal/netbind"
	"flowdrop/pkg/hub"
)

// scanSource is a mutable, race-free stand-in for netbind.Scan.
type scanSource struct {
	mu    sync.Mutex
	addrs []netbind.Addr
	err   error
	calls atomic.Int64
}

func (s *scanSource) scan() ([]netbind.Addr, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return append([]netbind.Addr(nil), s.addrs...), nil
}

func (s *scanSource) set(addrs ...netbind.Addr) {
	s.mu.Lock()
	s.addrs = append([]netbind.Addr(nil), addrs...)
	s.mu.Unlock()
}

func (s *scanSource) fail(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

func lanAddr(t *testing.T, ip string) netbind.Addr {
	t.Helper()
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		t.Fatalf("parse %q: %v", ip, err)
	}
	return netbind.Addr{Iface: "eth0", IP: parsed, Kind: netbind.KindLAN}
}

// freePort reserves an ephemeral loopback port and releases it, so the Hub can
// bind 127.0.0.1:<port> and auto-bind 127.0.0.2:<port> on the same port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// autoBindOptions builds a Hub that auto-binds on an explicit, stable port.
func autoBindOptions(t *testing.T, src *scanSource) (hub.Options, string) {
	t.Helper()
	port := freePort(t)
	opts := testOptions(t, func(o *hub.Options) {
		o.Listen = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		yes := true
		o.AutoBind = &yes
		o.AutoBindInterval = 10 * time.Millisecond
		o.Scan = src.scan
	})
	return opts, strconv.Itoa(port)
}

func waitURLs(t *testing.T, h *hub.Hub, want int) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var last []string
	for time.Now().Before(deadline) {
		last = h.URLs()
		if len(last) == want {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("URLs() = %v after 2s, want %d entries", last, want)
	return nil
}

func waitDialFails(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return
		}
		conn.Close()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s is still accepting connections after 2s", addr)
}

func requireHealthOK(t *testing.T, base string) {
	t.Helper()
	status, body := health(t, base)
	if status != http.StatusOK {
		t.Fatalf("health %s = %d, want 200 (body: %s)", base, status, body)
	}
}

// 1. An eligible LAN address present at Start is bound on the primary port, is
// reachable over plain HTTP and passes the dynamic Host allowlist.
func TestAutoBindBindsEligibleAddressAtStartup(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"))
	opts, port := autoBindOptions(t, src)

	h := mustNew(t, opts)
	startHub(t, h)

	urls := h.URLs()
	if len(urls) != 2 {
		t.Fatalf("URLs() = %v, want the primary plus one auto-bound listener", urls)
	}
	if got, want := urls[0], "http://127.0.0.1:"+port; got != want {
		t.Fatalf("URLs()[0] = %q, want primary %q", got, want)
	}
	if got, want := urls[1], "http://127.0.0.2:"+port; got != want {
		t.Fatalf("URLs()[1] = %q, want auto-bound %q", got, want)
	}
	if h.URL() != urls[0] {
		t.Fatalf("URL() = %q, want primary %q", h.URL(), urls[0])
	}
	// 127.0.0.2 is not part of the static allowlist, so a 200 proves that
	// SetBoundHosts received the auto-bound address.
	requireHealthOK(t, urls[1])
	requireHealthOK(t, urls[0])
}

// 2. An address that appears while the Hub is running is bound by the rescan.
func TestAutoBindAddsAddressWhileRunning(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"))
	opts, port := autoBindOptions(t, src)

	h := mustNew(t, opts)
	startHub(t, h)
	if urls := h.URLs(); len(urls) != 2 {
		t.Fatalf("URLs() at start = %v, want 2 entries", urls)
	}

	src.set(lanAddr(t, "127.0.0.2"), lanAddr(t, "127.0.0.3"))
	urls := waitURLs(t, h, 3)
	if got, want := urls[2], "http://127.0.0.3:"+port; got != want {
		t.Fatalf("URLs()[2] = %q, want new address %q", got, want)
	}
	requireHealthOK(t, urls[2])
	requireHealthOK(t, urls[1])
}

// 3. An address that disappears loses its listener; the port stops accepting.
func TestAutoBindRemovesAddressWhileRunning(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"), lanAddr(t, "127.0.0.3"))
	opts, port := autoBindOptions(t, src)

	h := mustNew(t, opts)
	startHub(t, h)
	urls := waitURLs(t, h, 3)
	if urls[1] != "http://127.0.0.2:"+port || urls[2] != "http://127.0.0.3:"+port {
		t.Fatalf("URLs() = %v, want 127.0.0.2 then 127.0.0.3", urls)
	}

	src.set(lanAddr(t, "127.0.0.3"))
	urls = waitURLs(t, h, 2)
	if got, want := urls[1], "http://127.0.0.3:"+port; got != want {
		t.Fatalf("URLs()[1] = %q, want the surviving address %q", got, want)
	}
	waitDialFails(t, net.JoinHostPort("127.0.0.2", port))
	requireHealthOK(t, urls[1])
}

// 4. A scan that reports the primary listener's own address must not bind it a
// second time (EADDRINUSE) and must not disturb the Hub.
func TestAutoBindSkipsAlreadyBoundPrimaryAddress(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.1"))
	opts, port := autoBindOptions(t, src)

	h := mustNew(t, opts)
	startHub(t, h)
	if urls := waitURLs(t, h, 1); urls[0] != "http://127.0.0.1:"+port {
		t.Fatalf("URLs() = %v, want only the primary listener", urls)
	}
	// Give several rescan rounds a chance to (incorrectly) add it again.
	time.Sleep(60 * time.Millisecond)
	if urls := h.URLs(); len(urls) != 1 {
		t.Fatalf("URLs() = %v, want only the primary listener", urls)
	}
	requireHealthOK(t, "http://127.0.0.1:"+port)
}

// 5. An address this machine cannot bind (TEST-NET-1) is skipped with a warning:
// the Hub keeps serving and removing it again changes nothing.
func TestAutoBindSkipsUnbindableAddress(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "192.0.2.1"))
	opts, port := autoBindOptions(t, src)

	h := mustNew(t, opts)
	startHub(t, h)
	if urls := waitURLs(t, h, 1); urls[0] != "http://127.0.0.1:"+port {
		t.Fatalf("URLs() = %v, want only the primary listener", urls)
	}
	requireHealthOK(t, "http://127.0.0.1:"+port)

	src.set()
	time.Sleep(40 * time.Millisecond)
	if urls := h.URLs(); len(urls) != 1 {
		t.Fatalf("URLs() = %v after dropping the unbindable address, want 1", urls)
	}
	requireHealthOK(t, "http://127.0.0.1:"+port)
}

// 6. A failing scan is only logged: the current listener set and the service
// stay untouched.
func TestAutoBindScanErrorKeepsServing(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"))
	opts, port := autoBindOptions(t, src)
	src.fail(errors.New("boom"))

	h := mustNew(t, opts)
	startHub(t, h)
	if urls := waitURLs(t, h, 1); urls[0] != "http://127.0.0.1:"+port {
		t.Fatalf("URLs() = %v, want only the primary listener", urls)
	}
	time.Sleep(40 * time.Millisecond)
	if urls := h.URLs(); len(urls) != 1 {
		t.Fatalf("URLs() = %v after repeated scan errors, want 1", urls)
	}
	requireHealthOK(t, "http://127.0.0.1:"+port)
	if src.calls.Load() < 2 {
		t.Fatalf("Scan was called %d times, want the rescan loop to keep polling", src.calls.Load())
	}

	// Recovery: once the scan works again the address is bound.
	src.set(lanAddr(t, "127.0.0.2"))
	src.mu.Lock()
	src.err = nil
	src.mu.Unlock()
	if urls := waitURLs(t, h, 2); urls[1] != "http://127.0.0.2:"+port {
		t.Fatalf("URLs() = %v after the scan recovered, want 127.0.0.2 second", urls)
	}
}

// 7. With an ephemeral primary port auto binding is off by design: the port the
// OS picked cannot be reused on other interfaces.
func TestAutoBindDisabledForEphemeralPrimaryPort(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"))
	opts := testOptions(t, func(o *hub.Options) {
		o.Listen = "127.0.0.1:0"
		yes := true
		o.AutoBind = &yes
		o.AutoBindInterval = 10 * time.Millisecond
		o.Scan = src.scan
	})

	h := mustNew(t, opts)
	startHub(t, h)
	urls := h.URLs()
	if len(urls) != 1 {
		t.Fatalf("URLs() = %v, want only the primary listener for port 0", urls)
	}
	time.Sleep(40 * time.Millisecond)
	if urls := h.URLs(); len(urls) != 1 {
		t.Fatalf("URLs() = %v, want only the primary listener for port 0", urls)
	}
	requireHealthOK(t, urls[0])
}

// 8. Stop withdraws the auto-bound listeners, clears URLs() and ends the rescan
// loop so Scan is never called again.
func TestAutoBindStopsWithHub(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"))
	opts, port := autoBindOptions(t, src)

	h := mustNew(t, opts)
	startHub(t, h)
	if urls := waitURLs(t, h, 2); urls[1] != "http://127.0.0.2:"+port {
		t.Fatalf("URLs() = %v, want the auto-bound address second", urls)
	}

	stopQuietly(t, h)
	if urls := h.URLs(); urls != nil {
		t.Fatalf("URLs() after Stop = %v, want nil", urls)
	}
	waitDialFails(t, net.JoinHostPort("127.0.0.2", port))
	waitDialFails(t, net.JoinHostPort("127.0.0.1", port))

	after := src.calls.Load()
	time.Sleep(80 * time.Millisecond)
	if got := src.calls.Load(); got != after {
		t.Fatalf("Scan was called %d more times after Stop, want none", got-after)
	}
}

// 9. Regression guard: without AutoBind nothing is bound beyond the configured
// listeners even when eligible addresses exist.
func TestAutoBindDisabledKeepsSingleListener(t *testing.T) {
	src := &scanSource{}
	src.set(lanAddr(t, "127.0.0.2"), lanAddr(t, "127.0.0.3"))
	port := freePort(t)
	opts := testOptions(t, func(o *hub.Options) {
		o.Listen = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		o.AutoBindInterval = 10 * time.Millisecond
		o.Scan = src.scan
	})

	h := mustNew(t, opts)
	startHub(t, h)
	urls := h.URLs()
	if len(urls) != 1 {
		t.Fatalf("URLs() = %v, want only the primary listener when AutoBind is false", urls)
	}
	waitDialFails(t, net.JoinHostPort("127.0.0.2", strconv.Itoa(port)))
	if src.calls.Load() != 0 {
		t.Fatalf("Scan was called %d times without AutoBind, want 0", src.calls.Load())
	}
}

// probeBindable reports whether this machine can bind ip right now, i.e. the
// address really exists. Binding an address that does not belong to any local
// interface fails with "cannot assign requested address", which is what the
// container-bridge test has to tolerate.
func probeBindable(ip string) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(ip, "0"))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// 10. A container bridge address is auto-bound on purpose (LocalSend parity) and
// a bridge this machine does not have is skipped without disturbing the Hub. The
// address is deliberately absent from the mDNS advertisement set; that filter is
// covered by TestFilterAdvertised in autobind_internal_test.go, because mDNS is
// disabled in these hermetic tests.
func TestAutoBindBindsContainerBridgeAddress(t *testing.T) {
	src := &scanSource{}
	src.set(
		lanAddr(t, "127.0.0.2"),
		netbind.Addr{Iface: "docker0", IP: netip.MustParseAddr("172.17.0.1"), Kind: netbind.KindVirtual},
	)
	opts, port := autoBindOptions(t, src)

	// Decide the expectation before the Hub binds anything: on a machine with
	// docker0 the address must appear in URLs(), otherwise binding it fails and
	// only a warning is logged.
	bridgeExists := probeBindable("172.17.0.1")
	wantURLs := 2
	if bridgeExists {
		wantURLs = 3
	}

	h := mustNew(t, opts)
	startHub(t, h)

	primary := "http://127.0.0.1:" + port
	lanURL := "http://127.0.0.2:" + port
	bridgeURL := "http://172.17.0.1:" + port
	urls := waitURLs(t, h, wantURLs)
	if got := urls[0]; got != primary {
		t.Fatalf("URLs()[0] = %q, want primary %q", got, primary)
	}
	if got := urls[1]; got != lanURL {
		t.Fatalf("URLs()[1] = %q, want auto-bound %q", got, lanURL)
	}
	requireHealthOK(t, primary)
	requireHealthOK(t, lanURL)
	if bridgeExists {
		if got := urls[2]; got != bridgeURL {
			t.Fatalf("URLs()[2] = %q, want container bridge %q", got, bridgeURL)
		}
		requireHealthOK(t, bridgeURL)
	}

	// A bridge that cannot be bound must not crash, retry-loop into a growing
	// listener set, or stop the Hub.
	time.Sleep(60 * time.Millisecond)
	if got := h.URLs(); len(got) != wantURLs {
		t.Fatalf("URLs() = %v after several rescans, want %d entries", got, wantURLs)
	}
	requireHealthOK(t, primary)
}
