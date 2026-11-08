package hub

// Local pairing API of the Hub (docs/PAIRING.md, task PA-04H).
//
// Everything here is a purely local, in-process bridge for the desktop UI: no
// HTTP route generates a code, lists devices or revokes one, so a remote client
// can never administer pairing. The code itself is only returned to this
// process and is never logged.
//
// The hub's own identity (hub ID + endpoint list) is published to the API layer
// through a callback so /api/v1/health and the session response can advertise
// the real bound addresses without the API layer owning lifecycle state.

import (
	"errors"
	"net"
	"net/url"
	"strings"
	"time"

	"flowdrop/internal/api"
	"flowdrop/internal/pairing"
)

// PairingCode and PairedDevice are the frozen pairing types re-exported so the
// desktop bridge depends on the hub package only (no second import of the
// internal pairing store).
type (
	// PairingCode is a freshly generated 4-digit code with its expiry.
	PairingCode = pairing.Code
	// PairedDevice is one paired device as listed locally.
	PairedDevice = pairing.Device
)

// ErrNotStarted is returned by the local pairing entry points when the Hub has
// not bound its listeners yet. Pairing is a running-Hub feature; a Hub that is
// only configured must be Start()ed first.
var ErrNotStarted = errors.New("hub is not started")

// fallbackHubName is only used when the OS reports no usable hostname.
const fallbackHubName = "FlowDrop Hub"

// endpointKind classifies a bound URL for the API metadata (frozen values
// "lan", "tailscale", "other"). Only RFC1918 IPv4 is "lan"; 100.64.0.0/10 (the
// CGNAT range Tailscale uses) is "tailscale"; loopback, IPv6, link-local,
// hostnames and anything else are "other". No address is ever invented here and
// no tailscale CLI/API is consulted.
func endpointKind(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "other"
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return "other"
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return "other"
	}
	switch {
	case ip4[0] == 10:
		return "lan"
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return "lan"
	case ip4[0] == 192 && ip4[1] == 168:
		return "lan"
	case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127:
		// 100.64.0.0/10: Tailscale's CGNAT range (no interface check, so a
		// non-Tailscale CGNAT listener is classified the same way).
		return "tailscale"
	}
	return "other"
}

// hubInfo snapshots the metadata the API layer publishes. It is called on a
// request path, so it only takes the Hub mutex briefly and never touches the
// database: the stable ID is cached at New and the endpoints are the actual
// successfully bound listeners (never a configured-but-unbound address).
func (h *Hub) hubInfo() api.HubInfo {
	h.mu.Lock()
	id, name := h.hubID, h.hostname
	h.mu.Unlock()
	if name == "" {
		name = fallbackHubName
	}
	urls := h.URLs()
	endpoints := make([]api.HubEndpointInfo, 0, len(urls))
	for _, u := range urls {
		endpoints = append(endpoints, api.HubEndpointInfo{URL: u, Kind: endpointKind(u)})
	}
	return api.HubInfo{ID: id, Name: name, Endpoints: endpoints}
}

// localPairing returns the pairing store only for a started, live Hub. A Hub
// that was never started has no listening client to pair with, and a stopped
// Hub reports ErrClosed like the other lifecycle-aware methods.
func (h *Hub) localPairing() (*pairing.Store, error) {
	h.mu.Lock()
	stopped, started, store := h.stopped, h.started, h.pairing
	h.mu.Unlock()
	if stopped {
		return nil, ErrClosed
	}
	if !started || store == nil {
		return nil, ErrNotStarted
	}
	return store, nil
}

// CreatePairingCode generates a new 4-digit code (leading zeros kept) valid for
// 120 seconds and returns it for local display. Generating a new code
// invalidates any previous one, resets the accumulated wrong-code counter and
// unlocks a locked Hub. The code is never logged and never exposed over HTTP.
func (h *Hub) CreatePairingCode() (PairingCode, error) {
	store, err := h.localPairing()
	if err != nil {
		return PairingCode{}, err
	}
	return store.CreateCode(time.Now())
}

// ListPairedDevices returns the paired devices with their names and
// created/last-seen timestamps. The raw device tokens are never part of this
// view (the store only keeps hashes).
func (h *Hub) ListPairedDevices() ([]PairedDevice, error) {
	store, err := h.localPairing()
	if err != nil {
		return nil, err
	}
	return store.ListDevices()
}

// RevokePairedDevice revokes one paired device. The revocation goes through the
// API server so the existing HTTP session and any open WebSocket for that device
// are invalidated, not just the database row.
func (h *Hub) RevokePairedDevice(id string) error {
	if _, err := h.localPairing(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("device id must not be empty")
	}
	h.mu.Lock()
	srv := h.api
	h.mu.Unlock()
	if srv == nil {
		return ErrNotStarted
	}
	return srv.RevokeDevice(id)
}
