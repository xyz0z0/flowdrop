package main

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"flowdrop/desktop/internal/lanbrowse"
	"flowdrop/desktop/internal/tailnet"
)

// Hub discovery for the settings window.
//
// Two sources feed one list:
//
//	lan        mDNS (_flowdrop._tcp) on this machine's own network segment
//	tailscale  peers from `tailscale status --json`, probed on the Hub port
//
// Both are only address hints: every candidate is probed with the same
// reachability check ApplyMode uses (probeRemoteHub), unreachable entries are
// dropped, and a candidate never bypasses the Hub's own pairing flow. mDNS is
// link-local, so it can never find a Hub across networks; the tailnet source is
// what covers two machines behind different LANs that happen to use the same
// RFC1918 range.

const (
	hubSourceLAN       = "lan"
	hubSourceTailscale = "tailscale"

	// discoverBrowseTimeout is how long one mDNS browse runs before the list is
	// built. It is shorter than lanbrowse's own default so the whole discovery
	// call stays inside discoverTotalTimeout.
	discoverBrowseTimeout = 3 * time.Second
	// discoverTotalTimeout bounds the whole call: browse + status + probes.
	discoverTotalTimeout = 8 * time.Second
	// discoverMaxConcurrentProbes keeps a large tailnet from opening dozens of
	// sockets at once.
	discoverMaxConcurrentProbes = 6
	// defaultHubPort is used when the settings carry no port yet.
	defaultHubPort = 8080
)

// HubCandidate is one Hub address the settings window can offer.
type HubCandidate struct {
	// URL is the http(s) base URL that would be saved as the remote address.
	URL string `json:"url"`
	// Source is "lan" or "tailscale"; the window groups and labels by it.
	Source string `json:"source"`
	// Label is the human hint next to the address, such as the mDNS instance
	// name or the peer's host name. It is never proof of identity.
	Label string `json:"label"`
}

// discoverFuncs are the seams the tests replace. Production always uses the
// real browser, the real tailscale CLI and the real probe.
var (
	browseLANHubs = lanbrowse.Browse
	listTailnet   = tailnet.Peers
)

// DiscoverHubs probes both sources and returns the Hubs that answered, LAN
// first, then tailnet. A source that cannot run on this machine (no multicast
// interface, no tailscale CLI) contributes nothing; the call only fails when
// neither source could be consulted at all.
func (s *SettingsService) DiscoverHubs() ([]HubCandidate, error) {
	ctx, cancel := context.WithTimeout(context.Background(), discoverTotalTimeout)
	defer cancel()

	ports := s.hubPorts()

	candidates := make([]HubCandidate, 0, 8)
	seen := map[string]struct{}{}
	add := func(url, source, label string) {
		if _, dup := seen[url]; dup {
			return
		}
		seen[url] = struct{}{}
		candidates = append(candidates, HubCandidate{URL: url, Source: source, Label: label})
	}

	var lanErr error
	if lanHubs, err := browseLANHubs(ctx, discoverBrowseTimeout); err != nil {
		lanErr = err
	} else {
		for _, c := range lanHubs {
			add(c.URL, hubSourceLAN, c.Instance)
		}
	}

	tailErr := error(nil)
	if peers, err := listTailnet(ctx); err != nil {
		if !tailnet.ErrUnavailable(err) {
			tailErr = err
		}
	} else {
		for _, peer := range peers {
			for _, port := range ports {
				add(hubURL(peer.IP, port), hubSourceTailscale, peerLabel(peer))
			}
		}
	}

	if lanErr != nil && tailErr != nil && len(candidates) == 0 {
		return nil, errors.New(lanErr.Error() + "；" + tailErr.Error())
	}

	reachable := probeCandidates(ctx, candidates)
	sort.SliceStable(reachable, func(i, j int) bool {
		if reachable[i].Source != reachable[j].Source {
			return reachable[i].Source == hubSourceLAN
		}
		return reachable[i].URL < reachable[j].URL
	})
	return reachable, nil
}

// hubPorts is the set of ports a remote Hub is assumed to listen on: the port
// this desktop is configured with (the project default is the same everywhere)
// plus the built-in 8080. Duplicates are removed so one peer is not probed
// twice on the same port.
func (s *SettingsService) hubPorts() []int {
	port := defaultHubPort
	if s != nil {
		if st, err := s.GetState(); err == nil && st.Port > 0 {
			port = st.Port
		}
	}
	ports := []int{port}
	if port != defaultHubPort {
		ports = append(ports, defaultHubPort)
	}
	return ports
}

// peerLabel renders the hint shown next to a tailnet candidate.
func peerLabel(peer tailnet.Peer) string {
	name := peer.Name
	if name == "" {
		name = peer.IP
	}
	if peer.Online {
		return name + "（Tailscale）"
	}
	return name + "（Tailscale，离线）"
}

// hubURL builds the base URL for a candidate address.
func hubURL(host string, port int) string {
	return "http://" + strings.TrimSpace(host) + ":" + strconv.Itoa(port)
}

// probeCandidates keeps the candidates whose address actually answers. Probes
// run concurrently but bounded, and each has the probe deadline ApplyMode uses,
// so an unreachable peer cannot stall the whole call.
func probeCandidates(ctx context.Context, candidates []HubCandidate) []HubCandidate {
	if len(candidates) == 0 {
		return nil
	}
	type result struct {
		index int
		ok    bool
	}
	sem := make(chan struct{}, discoverMaxConcurrentProbes)
	results := make(chan result, len(candidates))
	for i, c := range candidates {
		go func(i int, url string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			err := probeRemoteHub(ctx, url)
			results <- result{index: i, ok: err == nil}
		}(i, c.URL)
	}
	ok := make(map[int]bool, len(candidates))
	for range candidates {
		r := <-results
		if r.ok {
			ok[r.index] = true
		}
	}
	out := make([]HubCandidate, 0, len(ok))
	for i, c := range candidates {
		if ok[i] {
			out = append(out, c)
		}
	}
	return out
}
