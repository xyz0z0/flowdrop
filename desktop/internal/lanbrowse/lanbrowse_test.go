package lanbrowse

import (
	"testing"
	"time"
)

func TestParseAcceptsHubAdvertisement(t *testing.T) {
	c, ok := Parse("_flowdrop._tcp", "FlowDrop-bob-pc", 8080, "192.168.1.20", []string{"v=1", "scheme=http", "path=/"})
	if !ok {
		t.Fatal("Parse rejected a valid advertisement")
	}
	if c.URL != "http://192.168.1.20:8080" {
		t.Fatalf("URL = %q, want http://192.168.1.20:8080", c.URL)
	}
	if c.Host != "192.168.1.20" || c.Port != 8080 || c.Instance != "FlowDrop-bob-pc" {
		t.Fatalf("unexpected candidate: %+v", c)
	}
}

func TestParseAcceptsServiceWithDotAndDomain(t *testing.T) {
	for _, service := range []string{"_flowdrop._tcp.", "_flowdrop._tcp.local.", "_FLOWDROP._TCP"} {
		if _, ok := Parse(service, "FlowDrop-x", 8080, "10.0.0.5", nil); !ok {
			t.Fatalf("Parse rejected service %q", service)
		}
	}
}

func TestParseToleratesMissingTXT(t *testing.T) {
	if _, ok := Parse("_flowdrop._tcp", "FlowDrop-x", 8080, "10.0.0.5", nil); !ok {
		t.Fatal("Parse rejected an advertisement without TXT")
	}
}

func TestParseRejectsForeignRecords(t *testing.T) {
	cases := []struct {
		name     string
		service  string
		instance string
		port     int
		host     string
		txt      []string
	}{
		{"wrong service", "_http._tcp", "FlowDrop-x", 8080, "10.0.0.5", nil},
		{"wrong instance prefix", "_flowdrop._tcp", "SomeHub", 8080, "10.0.0.5", nil},
		{"port zero", "_flowdrop._tcp", "FlowDrop-x", 0, "10.0.0.5", nil},
		{"port too large", "_flowdrop._tcp", "FlowDrop-x", 70000, "10.0.0.5", nil},
		{"loopback host", "_flowdrop._tcp", "FlowDrop-x", 8080, "127.0.0.1", nil},
		{"tailscale host", "_flowdrop._tcp", "FlowDrop-x", 8080, "100.125.168.63", nil},
		{"public host", "_flowdrop._tcp", "FlowDrop-x", 8080, "203.0.113.9", nil},
		{"host name", "_flowdrop._tcp", "FlowDrop-x", 8080, "hub.local", nil},
		{"https txt", "_flowdrop._tcp", "FlowDrop-x", 8080, "10.0.0.5", []string{"v=1", "scheme=https", "path=/"}},
		{"wrong version", "_flowdrop._tcp", "FlowDrop-x", 8080, "10.0.0.5", []string{"v=2", "scheme=http", "path=/"}},
		{"wrong path", "_flowdrop._tcp", "FlowDrop-x", 8080, "10.0.0.5", []string{"v=1", "scheme=http", "path=/inbox"}},
		{"missing scheme", "_flowdrop._tcp", "FlowDrop-x", 8080, "10.0.0.5", []string{"v=1", "path=/"}},
	}
	for _, tc := range cases {
		if c, ok := Parse(tc.service, tc.instance, tc.port, tc.host, tc.txt); ok {
			t.Fatalf("%s: Parse accepted %+v, want rejection", tc.name, c)
		}
	}
}

func TestParseAcceptsPrivateRanges(t *testing.T) {
	for _, host := range []string{"10.1.2.3", "172.16.5.9", "172.31.255.254", "192.168.31.184"} {
		if _, ok := Parse("_flowdrop._tcp", "FlowDrop-x", 8080, host, nil); !ok {
			t.Fatalf("Parse rejected LAN host %q", host)
		}
	}
}

func TestCandidateString(t *testing.T) {
	c := Candidate{URL: "http://10.0.0.5:8080", Instance: "FlowDrop-bob-pc"}
	if got := c.String(); got != "http://10.0.0.5:8080（FlowDrop-bob-pc）" {
		t.Fatalf("String() = %q", got)
	}
	empty := Candidate{URL: "http://10.0.0.5:8080"}
	if got := empty.String(); got != "http://10.0.0.5:8080" {
		t.Fatalf("String() without instance = %q", got)
	}
}

func TestNormalizeService(t *testing.T) {
	for _, in := range []string{"_flowdrop._tcp", "_flowdrop._tcp.", "_flowdrop._tcp.local.", " _FLOWDROP._TCP. "} {
		if got := normalizeService(in); got != ServiceType {
			t.Fatalf("normalizeService(%q) = %q, want %q", in, got, ServiceType)
		}
	}
}

func TestDefaultBrowseTimeoutIsUsable(t *testing.T) {
	if DefaultBrowseTimeout <= 0 || DefaultBrowseTimeout > 10*time.Second {
		t.Fatalf("DefaultBrowseTimeout = %v, want a short positive window", DefaultBrowseTimeout)
	}
}
