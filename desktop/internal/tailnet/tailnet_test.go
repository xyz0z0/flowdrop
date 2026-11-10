package tailnet

import (
	"encoding/json"
	"net"
	"testing"
)

func TestParseStatusKeepsOnlineAndOfflinePeers(t *testing.T) {
	raw := []byte(`{
	  "Peer": {
	    "nodekey:aaa": {"HostName": "linux-box", "DNSName": "linux-box.tail.ts.net.", "OS": "linux", "Online": true, "TailscaleIPs": ["100.125.168.63", "fd7a:115c:a1e0::1"]},
	    "nodekey:bbb": {"HostName": "old-laptop", "DNSName": "old-laptop.tail.ts.net.", "OS": "windows", "Online": false, "TailscaleIPs": ["100.86.13.51"]},
	    "nodekey:ccc": {"HostName": "no-cgnat", "OS": "linux", "Online": true, "TailscaleIPs": ["192.168.1.9"]}
	  }
	}`)
	peers, err := ParseStatus(raw)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2 (peer without a 100.64/10 address is dropped): %+v", len(peers), peers)
	}
	if peers[0].Name != "linux-box" || peers[0].IP != "100.125.168.63" || !peers[0].Online || peers[0].OS != "linux" {
		t.Fatalf("first peer = %+v", peers[0])
	}
	if peers[1].Name != "old-laptop" || peers[1].Online {
		t.Fatalf("offline peer = %+v", peers[1])
	}
}

func TestParseStatusFallsBackForNames(t *testing.T) {
	raw := []byte(`{"Peer": {
	  "a": {"DNSName": "only-dns.tail.ts.net.", "Online": true, "TailscaleIPs": ["100.64.0.1"]},
	  "b": {"Online": true, "TailscaleIPs": ["100.64.0.2"]}
	}}`)
	peers, err := ParseStatus(raw)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	byIP := map[string]Peer{}
	for _, p := range peers {
		byIP[p.IP] = p
	}
	if got := byIP["100.64.0.1"].Name; got != "only-dns" {
		t.Fatalf("DNSName fallback = %q, want only-dns", got)
	}
	if got := byIP["100.64.0.2"].Name; got != "100.64.0.2" {
		t.Fatalf("IP fallback = %q, want the address itself", got)
	}
}

func TestParseStatusRejectsMalformed(t *testing.T) {
	if _, err := ParseStatus([]byte("{not json")); err == nil {
		t.Fatal("ParseStatus accepted malformed JSON")
	}
	empty, err := ParseStatus(nil)
	if err != nil || empty != nil {
		t.Fatalf("ParseStatus(nil) = %v, %v; want nil, nil", empty, err)
	}
	none, err := ParseStatus([]byte(`{}`))
	if err != nil || len(none) != 0 {
		t.Fatalf("ParseStatus({}) = %v, %v; want empty", none, err)
	}
}

func TestParseStatusDeduplicatesAddresses(t *testing.T) {
	raw := []byte(`{"Peer": {
	  "a": {"HostName": "one", "TailscaleIPs": ["100.70.0.5"]},
	  "b": {"HostName": "two", "TailscaleIPs": ["100.70.0.5"]}
	}}`)
	peers, err := ParseStatus(raw)
	if err != nil {
		t.Fatalf("ParseStatus: %v", err)
	}
	if len(peers) != 1 || peers[0].IP != "100.70.0.5" {
		t.Fatalf("duplicate addresses not removed: %+v", peers)
	}
}

func TestIsCGNAT(t *testing.T) {
	in := []string{"100.64.0.0", "100.125.168.63", "100.127.255.255"}
	out := []string{"100.63.255.255", "100.128.0.0", "192.168.1.1", "10.0.0.1"}
	for _, s := range in {
		if !IsCGNAT(net.ParseIP(s).To4()) {
			t.Fatalf("IsCGNAT(%s) = false, want true", s)
		}
	}
	for _, s := range out {
		if IsCGNAT(net.ParseIP(s).To4()) {
			t.Fatalf("IsCGNAT(%s) = true, want false", s)
		}
	}
	if IsCGNAT(net.ParseIP("fd7a:115c:a1e0::1")) {
		t.Fatal("IsCGNAT(IPv6) = true, want false")
	}
}

func TestErrUnavailableIsRecognisable(t *testing.T) {
	if !ErrUnavailable(errNoCommand) {
		t.Fatal("ErrUnavailable(errNoCommand) = false")
	}
	var parseErr error
	if err := json.Unmarshal([]byte("{"), &struct{}{}); err == nil {
		t.Fatal("expected a decode error to build the case")
	} else {
		parseErr = err
	}
	if ErrUnavailable(parseErr) {
		t.Fatal("ErrUnavailable(unrelated error) = true, want false")
	}
}
