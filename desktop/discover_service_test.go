package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"flowdrop/desktop/internal/lanbrowse"
	"flowdrop/desktop/internal/tailnet"
)

// stubDiscovery replaces the mDNS browser, the tailscale peer list and the
// reachability probe, so one test can describe a whole network without
// touching the real one.
func stubDiscovery(t *testing.T, lan []lanbrowse.Candidate, lanErr error, peers []tailnet.Peer, peerErr error, probe func(string) error) {
	t.Helper()
	prevBrowse, prevTailnet, prevProbe := browseLANHubs, listTailnet, probeRemoteHub
	browseLANHubs = func(context.Context, time.Duration) ([]lanbrowse.Candidate, error) { return lan, lanErr }
	listTailnet = func(context.Context) ([]tailnet.Peer, error) { return peers, peerErr }
	probeRemoteHub = func(_ context.Context, url string) error {
		if probe == nil {
			return nil
		}
		return probe(url)
	}
	t.Cleanup(func() {
		browseLANHubs, listTailnet, probeRemoteHub = prevBrowse, prevTailnet, prevProbe
	})
}

func TestDiscoverHubsMergesBothSources(t *testing.T) {
	svc, _, _ := newTestService(t)
	stubDiscovery(t,
		[]lanbrowse.Candidate{{URL: "http://192.168.1.20:8080", Instance: "FlowDrop-office"}}, nil,
		[]tailnet.Peer{{Name: "linux-box", IP: "100.125.168.63", Online: true}}, nil,
		nil)
	got, err := svc.DiscoverHubs()
	if err != nil {
		t.Fatalf("DiscoverHubs: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("DiscoverHubs = %+v, want the LAN Hub and at least one tailnet candidate", got)
	}
	if got[0].Source != hubSourceLAN {
		t.Fatalf("first candidate source = %q, want lan first", got[0].Source)
	}
	lanURL := "http://192.168.1.20:8080"
	if got[0].URL != lanURL || got[0].Label != "FlowDrop-office" {
		t.Fatalf("LAN candidate = %+v", got[0])
	}
	found := false
	for _, c := range got[1:] {
		if c.Source != hubSourceTailscale {
			continue
		}
		if c.URL == "http://100.125.168.63:8080" {
			found = true
			if c.Label == "" {
				t.Fatalf("tailnet candidate has no label: %+v", c)
			}
		}
	}
	if !found {
		t.Fatalf("tailnet peer on the hub port missing from %+v", got)
	}
}

func TestDiscoverHubsDropsUnreachableCandidates(t *testing.T) {
	svc, _, _ := newTestService(t)
	stubDiscovery(t,
		[]lanbrowse.Candidate{
			{URL: "http://192.168.1.20:8080", Instance: "FlowDrop-reachable"},
			{URL: "http://192.168.1.21:8080", Instance: "FlowDrop-gone"},
		}, nil, nil, nil,
		func(url string) error {
			if url == "http://192.168.1.21:8080" {
				return errors.New("connection refused")
			}
			return nil
		})
	got, err := svc.DiscoverHubs()
	if err != nil {
		t.Fatalf("DiscoverHubs: %v", err)
	}
	if len(got) != 1 || got[0].URL != "http://192.168.1.20:8080" {
		t.Fatalf("DiscoverHubs = %+v, want only the answering Hub", got)
	}
}

func TestDiscoverHubsDeduplicatesAndHandlesRepeatedPorts(t *testing.T) {
	svc, _, _ := newTestService(t)
	lan := []lanbrowse.Candidate{
		{URL: "http://192.168.1.20:8080", Instance: "FlowDrop-a"},
		{URL: "http://192.168.1.20:8080", Instance: "FlowDrop-a"},
	}
	stubDiscovery(t, lan, nil, []tailnet.Peer{{Name: "peer", IP: "192.168.1.20", Online: true}}, nil, nil)
	got, err := svc.DiscoverHubs()
	if err != nil {
		t.Fatalf("DiscoverHubs: %v", err)
	}
	seen := map[string]int{}
	for _, c := range got {
		seen[c.URL]++
	}
	for url, n := range seen {
		if n != 1 {
			t.Fatalf("%s appears %d times, want 1", url, n)
		}
	}
}

func TestDiscoverHubsWithoutTailscaleCLIStaysSilent(t *testing.T) {
	svc, _, _ := newTestService(t)
	// tailnet.Peers on a machine without the CLI returns its own sentinel; the
	// discovery call must not surface that as an error.
	stubDiscovery(t, []lanbrowse.Candidate{{URL: "http://192.168.1.20:8080", Instance: "FlowDrop-a"}}, nil,
		nil, errors.New("tailscale 命令不可用"), nil)
	// The stub returns a plain error, which is not the tailnet sentinel, so this
	// case documents the tolerated path: LAN results are still returned.
	got, err := svc.DiscoverHubs()
	if err != nil {
		t.Fatalf("DiscoverHubs: %v", err)
	}
	if len(got) != 1 || got[0].Source != hubSourceLAN {
		t.Fatalf("DiscoverHubs = %+v, want the LAN candidate only", got)
	}
}

func TestDiscoverHubsReportsWhenNoSourceWorks(t *testing.T) {
	svc, _, _ := newTestService(t)
	stubDiscovery(t, nil, errors.New("局域网发现不可用：no multicast"), nil,
		errors.New("tailscale status: exit status 1"), nil)
	if _, err := svc.DiscoverHubs(); err == nil {
		t.Fatal("DiscoverHubs returned no error although neither source worked")
	}
}

func TestDiscoverHubsReturnsEmptyListInsteadOfError(t *testing.T) {
	svc, _, _ := newTestService(t)
	stubDiscovery(t, nil, nil, nil, errors.New("tailscale 命令不可用"), nil)
	got, err := svc.DiscoverHubs()
	if err != nil {
		t.Fatalf("DiscoverHubs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("DiscoverHubs = %+v, want an empty list", got)
	}
}

func TestHubPortsIncludesConfiguredAndDefault(t *testing.T) {
	svc, _, _ := newTestService(t)
	ports := svc.hubPorts()
	if len(ports) == 0 || ports[0] == 0 {
		t.Fatalf("hubPorts = %v", ports)
	}
	seen := map[int]bool{}
	for _, p := range ports {
		if p <= 0 || p > 65535 {
			t.Fatalf("hubPorts contains an invalid port: %v", ports)
		}
		if seen[p] {
			t.Fatalf("hubPorts contains %d twice: %v", p, ports)
		}
		seen[p] = true
	}
	if !seen[defaultHubPort] {
		t.Fatalf("hubPorts = %v, want the default port %d included", ports, defaultHubPort)
	}
}

func TestHubURLCarriesSchemePortAndHost(t *testing.T) {
	if got := hubURL("100.125.168.63", 8080); got != "http://100.125.168.63:8080" {
		t.Fatalf("hubURL = %q", got)
	}
}

func TestPeerLabelMarksOfflinePeers(t *testing.T) {
	online := peerLabel(tailnet.Peer{Name: "box", Online: true})
	offline := peerLabel(tailnet.Peer{Name: "box", Online: false})
	if online == offline {
		t.Fatalf("online and offline labels are identical: %q", online)
	}
}
