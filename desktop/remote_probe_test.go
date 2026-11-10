package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"flowdrop/desktop/internal/appsettings"
	"flowdrop/desktop/internal/network"
)

// stubRemoteProbe replaces the reachability probe for one test, so neither the
// service nor the window ever touches a real remote address.
func stubRemoteProbe(t *testing.T, err error) {
	t.Helper()
	prev := probeRemoteHub
	probeRemoteHub = func(context.Context, string) error { return err }
	t.Cleanup(func() { probeRemoteHub = prev })
}

func TestDefaultProbeRemoteHubReachable(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>inbox</html>"))
	}))
	defer srv.Close()

	if err := defaultProbeRemoteHub(context.Background(), srv.URL); err != nil {
		t.Fatalf("probe of a live server: %v", err)
	}
	if method != http.MethodGet || path != "/" {
		t.Fatalf("probe = %s %s, want GET /", method, path)
	}
	// A trailing slash in the saved address must not double up.
	if err := defaultProbeRemoteHub(context.Background(), srv.URL+"/"); err != nil {
		t.Fatalf("probe with a trailing slash: %v", err)
	}
}

func TestRemoteProbeUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	closedURL := srv.URL
	srv.Close()

	err := defaultProbeRemoteHub(context.Background(), closedURL)
	if err == nil {
		t.Fatalf("probe of a closed server at %s = nil error, want a connection error", closedURL)
	}
	msg := remoteProbeFailureMessage(closedURL, err)
	if strings.Contains(msg, "%!") {
		t.Fatalf("failure message has a formatting error: %s", msg)
	}
	if !strings.Contains(msg, closedURL) {
		t.Fatalf("failure message %q does not name the target %s", msg, closedURL)
	}
}

func TestRemoteProbeTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		srv.Close()
	}()

	start := time.Now()
	err := defaultProbeRemoteHub(context.Background(), srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("probe of a hanging server = nil error, want a timeout")
	}
	if elapsed < remoteProbeTimeout {
		t.Fatalf("probe returned after %v, want the full %v timeout", elapsed, remoteProbeTimeout)
	}
	if elapsed >= 3*time.Second {
		t.Fatalf("probe took %v, want about the %v timeout", elapsed, remoteProbeTimeout)
	}
	msg := remoteProbeFailureMessage(srv.URL, err)
	if !strings.Contains(msg, "2 秒内没有响应") {
		t.Fatalf("timeout message = %q, want the 2s timeout wording", msg)
	}
}

func TestRemoteProbeStatusFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "hub down", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	err := defaultProbeRemoteHub(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("probe of a 503 = nil error, want a status error")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Fatalf("probe error = %q, want the status code", err)
	}
}

func TestRemoteSubnetConflict(t *testing.T) {
	lan := []network.Interface{
		{Name: "loopback", Address: "127.0.0.1", Kind: network.KindLoopback},
		{Name: "eth0", Address: "192.168.31.20", Kind: network.KindLAN},
	}
	msg := remoteSubnetConflict("http://192.168.31.184:8080", lan)
	if msg == "" {
		t.Fatal("same /24 as a local address = no warning, want one")
	}
	for _, want := range []string{"192.168.31.184", "192.168.31.20", "192.168.31.0/24", "疑似", "Tailscale"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("warning %q does not contain %q", msg, want)
		}
	}

	if msg := remoteSubnetConflict("http://192.168.32.184:8080", lan); msg != "" {
		t.Fatalf("different /24 = %q, want no warning", msg)
	}
	if msg := remoteSubnetConflict("http://100.101.102.103:8080", lan); msg != "" {
		t.Fatalf("Tailscale/CGNAT address = %q, want no warning", msg)
	}
	if msg := remoteSubnetConflict("http://flowdrop.local:8080", lan); msg != "" {
		t.Fatalf("host name = %q, want no warning", msg)
	}
	if msg := remoteSubnetConflict("http://[fd7a:115c:a1e0::1]:8080", lan); msg != "" {
		t.Fatalf("IPv6 address = %q, want no warning", msg)
	}
	loopbackOnly := []network.Interface{
		{Name: "loopback", Address: "127.0.0.1", Kind: network.KindLoopback},
	}
	if msg := remoteSubnetConflict("http://127.0.0.1:8080", loopbackOnly); msg != "" {
		t.Fatalf("loopback-only machine = %q, want no warning", msg)
	}
	// Two local addresses in the same /24: only the first is reported.
	two := []network.Interface{
		{Name: "eth0", Address: "10.1.2.5", Kind: network.KindLAN},
		{Name: "eth1", Address: "10.1.2.6", Kind: network.KindLAN},
	}
	msg = remoteSubnetConflict("http://10.1.2.99:8080", two)
	if !strings.Contains(msg, "10.1.2.5") || strings.Contains(msg, "10.1.2.6") {
		t.Fatalf("warning %q, want the first local address only", msg)
	}
}

// TestServiceApplyModeRemoteProbeFailure proves a dead address is rejected
// before anything is stopped or saved, and that the message points at Tailscale.
func TestServiceApplyModeRemoteProbeFailure(t *testing.T) {
	svc, m, _ := newTestService(t)
	localURL := m.URL()
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")
	// Write the local mode the process is already running, so "unchanged" has a
	// file to be compared against.
	if _, err := svc.ApplyMode(appsettings.ModeLocal, ""); err != nil {
		t.Fatalf("ApplyMode(local): %v", err)
	}
	stubRemoteProbe(t, errors.New("dial tcp 192.168.31.184:8080: connect: connection refused"))

	remoteURL := "http://192.168.31.184:8080"
	if _, err := svc.ApplyMode(appsettings.ModeRemote, remoteURL); err == nil {
		t.Fatal("ApplyMode(remote) with a failing probe = nil error, want one")
	} else {
		msg := err.Error()
		if !strings.Contains(msg, "Tailscale") && !strings.Contains(msg, "100.64") {
			t.Fatalf("error %q does not mention Tailscale or 100.64", msg)
		}
		if !strings.Contains(msg, "没有改变") {
			t.Fatalf("error %q does not say the mode and settings are unchanged", msg)
		}
		if !strings.Contains(msg, remoteURL) {
			t.Fatalf("error %q does not name the target %s", msg, remoteURL)
		}
	}
	if got := desktop.remoteHubURL(); got != "" {
		t.Fatalf("failed ApplyMode(remote) switched the runtime mode to %q", got)
	}
	if got := m.URL(); got != localURL {
		t.Fatalf("failed ApplyMode(remote) touched the local Hub: %s -> %q", localURL, got)
	}
	saved, found, err := appsettings.Load(savedPath)
	if err != nil || !found {
		t.Fatalf("persisted settings = found=%v err=%v", found, err)
	}
	if saved.Mode != appsettings.ModeLocal || saved.RemoteURL != "" {
		t.Fatalf("failed ApplyMode(remote) changed the saved settings: %+v", saved)
	}
}

// TestServiceApplyModeRemoteProbeSuccess proves a reachable address is saved
// exactly as before (normalised, no trailing slash). It deliberately keeps the
// real probe so the wiring ApplyMode -> probeRemoteHub -> <remote>/ is exercised
// end to end against a live server.
func TestServiceApplyModeRemoteProbeSuccess(t *testing.T) {
	svc, m, _ := newTestService(t)
	savedPath := os.Getenv("FLOWDROP_DESKTOP_CONFIG")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("probe path = %q, want /", r.URL.Path)
		}
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st, err := svc.ApplyMode(appsettings.ModeRemote, srv.URL+"/")
	if err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("ApplyMode saved a remote address without probing it")
	}
	if st.Mode != appsettings.ModeRemote || st.RemoteURL != srv.URL {
		t.Fatalf("state = %+v, want remote mode at %s", st, srv.URL)
	}
	if got := desktop.remoteHubURL(); got != srv.URL {
		t.Fatalf("runtime remote URL = %q, want %q", got, srv.URL)
	}
	if got := m.URL(); got != "" {
		t.Fatalf("local Hub still running at %q after switching to remote", got)
	}
	saved, found, err := appsettings.Load(savedPath)
	if err != nil || !found {
		t.Fatalf("persisted settings = found=%v err=%v", found, err)
	}
	if saved.Mode != appsettings.ModeRemote || saved.RemoteURL != srv.URL {
		t.Fatalf("persisted settings = %+v, want remote mode and the trimmed URL", saved)
	}
}

// TestStateWarnsWhenRemoteSharesLocalSubnet checks the window hint against a
// real local address; machines without one (loopback only) cannot show it.
func TestStateWarnsWhenRemoteSharesLocalSubnet(t *testing.T) {
	svc, m, _ := newTestService(t)
	list, err := network.List()
	if err != nil {
		t.Fatalf("network.List: %v", err)
	}
	local := ""
	for _, i := range list {
		if i.Kind == network.KindLoopback {
			continue
		}
		ip := net.ParseIP(i.Address)
		if ip == nil || ip.To4() == nil || !network.IsLAN(ip) {
			continue
		}
		local = i.Address
		break
	}
	if local == "" {
		t.Skip("no non-loopback private IPv4 address on this machine")
	}
	target := local
	if ip := net.ParseIP(local).To4(); ip != nil {
		target = net.IPv4(ip[0], ip[1], ip[2], 99).String()
	}
	stubRemoteProbe(t, nil)
	if _, err := svc.ApplyMode(appsettings.ModeRemote, "http://"+target+":8080"); err != nil {
		t.Fatalf("ApplyMode(remote): %v", err)
	}
	st, err := svc.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.Mode != appsettings.ModeRemote {
		t.Fatalf("state = %+v, want remote mode", st)
	}
	if st.RemoteWarning == "" {
		t.Fatalf("remote_warning empty for %s next to local %s", target, local)
	}
	if !strings.Contains(st.RemoteWarning, local) {
		t.Fatalf("remote_warning = %q, does not name the local address %s", st.RemoteWarning, local)
	}
	// Switching back to local clears the hint.
	if _, err := svc.ApplyMode(appsettings.ModeLocal, ""); err != nil {
		t.Fatalf("ApplyMode(local): %v", err)
	}
	st, err = svc.GetState()
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if st.RemoteWarning != "" {
		t.Fatalf("remote_warning = %q in local mode, want empty", st.RemoteWarning)
	}
	if got := m.URL(); got == "" {
		t.Fatal("local Hub not running after switching back")
	}
}
