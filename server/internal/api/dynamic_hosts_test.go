// Dynamic Host allow-list tests: the Hub can bind new addresses at runtime, and
// SetBoundHosts must make (and unmake) those Host values acceptable without
// weakening the static config-derived set.
package api

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// hostStatus issues a GET /api/v1/health with an explicit Host header. Health
// needs no authentication, so the only gate under test is hostCheck.
func hostStatus(t *testing.T, client *http.Client, baseURL, host string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/health", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request with Host %q: %v", host, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body for Host %q: %v", host, err)
	}
	return resp.StatusCode, string(body)
}

func requireStatus(t *testing.T, client *http.Client, baseURL, host string, want int) {
	t.Helper()
	got, body := hostStatus(t, client, baseURL, host)
	if got != want {
		t.Fatalf("Host %q: status = %d, want %d (body: %s)", host, got, want, body)
	}
}

// A Host that is neither configured nor bound must stay rejected; this is the
// baseline that proves the change did not widen the allow-list.
func TestHostCheckRejectsUnlistedHost(t *testing.T) {
	h := newHarness(t)
	status, body := hostStatus(t, h.client, h.srv.URL, "192.168.50.10:1234")
	if status != http.StatusMisdirectedRequest {
		t.Fatalf("status = %d, want %d (body: %s)", status, http.StatusMisdirectedRequest, body)
	}
	if !strings.Contains(body, "host_not_allowed") {
		t.Fatalf("body = %q, want host_not_allowed", body)
	}
}

// SetBoundHosts admits a bound address (with or without port, any case) and an
// empty slice takes it away again, while the static loopback names survive.
func TestSetBoundHostsAdmitsAndClears(t *testing.T) {
	h := newHarness(t)

	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:1234", http.StatusMisdirectedRequest)

	h.api.SetBoundHosts([]string{"192.168.50.10"})
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:1234", http.StatusOK)
	// Port and case are normalized away, exactly like hostCheck normalizes.
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:8080", http.StatusOK)
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10", http.StatusOK)
	// Static loopback entries are unaffected by the dynamic set.
	requireStatus(t, h.client, h.srv.URL, "localhost:1234", http.StatusOK)

	// Replacing the set wholesale drops the previous entry.
	h.api.SetBoundHosts([]string{"10.0.0.5"})
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:1234", http.StatusMisdirectedRequest)
	requireStatus(t, h.client, h.srv.URL, "10.0.0.5:1234", http.StatusOK)

	// nil clears the dynamic set; loopback stays valid.
	h.api.SetBoundHosts(nil)
	requireStatus(t, h.client, h.srv.URL, "10.0.0.5:1234", http.StatusMisdirectedRequest)
	requireStatus(t, h.client, h.srv.URL, "localhost:1234", http.StatusOK)

	// An empty (non-nil) slice clears it too.
	h.api.SetBoundHosts([]string{"192.168.50.10"})
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:1234", http.StatusOK)
	h.api.SetBoundHosts([]string{})
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:1234", http.StatusMisdirectedRequest)
}

// Wildcard binds and empty/garbage values must never open the allow-list.
func TestSetBoundHostsIgnoresWildcardsAndEmpty(t *testing.T) {
	h := newHarness(t)

	h.api.SetBoundHosts([]string{"0.0.0.0", "::", "", "0.0.0.0:8080", "http://example.com/host", " "})
	requireStatus(t, h.client, h.srv.URL, "203.0.113.9", http.StatusMisdirectedRequest)
	requireStatus(t, h.client, h.srv.URL, "203.0.113.9:8080", http.StatusMisdirectedRequest)
	requireStatus(t, h.client, h.srv.URL, "example.com", http.StatusMisdirectedRequest)
	requireStatus(t, h.client, h.srv.URL, "0.0.0.0", http.StatusMisdirectedRequest)

	// A real entry next to the wildcards still lands.
	h.api.SetBoundHosts([]string{"0.0.0.0", "::", "", "192.168.50.10"})
	requireStatus(t, h.client, h.srv.URL, "192.168.50.10:8080", http.StatusOK)
	requireStatus(t, h.client, h.srv.URL, "203.0.113.9", http.StatusMisdirectedRequest)
}

// Concurrent SetBoundHosts calls with serving requests must be race-free and
// must only ever produce the allowed or the rejected status.
func TestSetBoundHostsConcurrentWithRequests(t *testing.T) {
	h := newHarness(t)

	const (
		flips    = 200
		requests = 200
	)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < flips; i++ {
			if i%2 == 0 {
				h.api.SetBoundHosts([]string{"192.168.50.10"})
			} else {
				h.api.SetBoundHosts(nil)
			}
		}
	}()

	go func() {
		defer wg.Done()
		req, err := http.NewRequest(http.MethodGet, h.srv.URL+"/api/v1/health", nil)
		if err != nil {
			t.Errorf("new request: %v", err)
			return
		}
		req.Host = "192.168.50.10:8080"
		for i := 0; i < requests; i++ {
			resp, err := h.client.Do(req.Clone(req.Context()))
			if err != nil {
				t.Errorf("request %d: %v", i, err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMisdirectedRequest {
				t.Errorf("request %d: status = %d, want 200 or 421 (body: %s)",
					i, resp.StatusCode, body)
				return
			}
		}
	}()

	wg.Wait()
}
