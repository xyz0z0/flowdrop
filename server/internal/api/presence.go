package api

import (
	"net/http"
	"sort"
	"sync"
	"time"
)

// presenceWindow is the fixed coalescing window for presence.changed. It is not
// a trailing debounce: a burst of joins/leaves publishes at most one event per
// window and the event is never postponed, so clients are not kept waiting and
// opening the list in many tabs does not multiply notifications.
const presenceWindow = 500 * time.Millisecond

// connID identifies one live event stream inside the presence registry.
type connID uint64

// presence is the in-memory registry of live event streams, keyed only by the
// paired device ID: deviceID -> set of connection IDs. It holds no name, token,
// IP or timestamp; the device name always comes from the pairing store when a
// snapshot is built, and a device with no live stream is simply absent.
type presence struct {
	mu       sync.Mutex
	nextConn connID
	byDevice map[string]map[connID]struct{}
	// pending is the single scheduled coalescing timer; nil when idle.
	pending *time.Timer
	window  time.Duration
	// publish is the invalidation broadcast (presence.changed). It carries no
	// device list: clients re-fetch GET /api/v1/presence.
	publish func()
}

func newPresence(window time.Duration, publish func()) *presence {
	return &presence{
		byDevice: make(map[string]map[connID]struct{}),
		window:   window,
		publish:  publish,
	}
}

// join registers one live connection for deviceID and returns its connection
// ID. The report says whether this was the device's first connection, which is
// the only join that is an observable presence change.
func (p *presence) join(deviceID string) (connID, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	conns := p.byDevice[deviceID]
	if conns == nil {
		conns = make(map[connID]struct{})
		p.byDevice[deviceID] = conns
	}
	p.nextConn++
	id := p.nextConn
	conns[id] = struct{}{}
	first := len(conns) == 1
	if first {
		p.scheduleLocked()
	}
	return id, first
}

// leave unregisters exactly one connection. Leaving twice (or leaving a
// connection a revocation already dropped) is a no-op, so it never removes a
// newer connection of the same device.
func (p *presence) leave(deviceID string, id connID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	conns, ok := p.byDevice[deviceID]
	if !ok {
		return
	}
	if _, present := conns[id]; !present {
		return
	}
	delete(conns, id)
	if len(conns) == 0 {
		delete(p.byDevice, deviceID)
		p.scheduleLocked()
	}
}

// drop unregisters every live connection of a revoked device so it disappears
// from the online list immediately instead of waiting for its socket to close.
// Connections registered before the revocation are removed because the whole
// device is revoked; a connection whose join happens after drop has taken the
// lock is left registered and unregisters itself when the 1s token revalidation
// closes its socket, so drop can never orphan or "adopt" a newer connection.
func (p *presence) drop(deviceID string) {
	if deviceID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	conns, ok := p.byDevice[deviceID]
	if !ok || len(conns) == 0 {
		return
	}
	delete(p.byDevice, deviceID)
	p.scheduleLocked()
}

// deviceOnline reports whether deviceID currently holds at least one live
// event stream.
func (p *presence) deviceOnline(deviceID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byDevice[deviceID]) > 0
}

// scheduleLocked arms the single coalescing timer. It must be called with mu
// held. A burst of changes inside the window collapses into the already armed
// timer; the timer is never rescheduled, so a busy Hub still publishes exactly
// once per window.
func (p *presence) scheduleLocked() {
	if p.pending != nil || p.publish == nil || p.window <= 0 {
		return
	}
	p.pending = time.AfterFunc(p.window, func() {
		p.mu.Lock()
		p.pending = nil
		p.mu.Unlock()
		// Published outside the lock: the broker must not run under it.
		p.publish()
	})
}

// presenceDevice is one entry of the online list. Only these three fields are
// ever exposed: no token, IP, connection count or last-seen time.
type presenceDevice struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

type presenceResponse struct {
	Online []presenceDevice `json:"online"`
}

// handlePresence lists the paired, non-revoked devices that currently hold a
// live event stream, in name then id order. current marks the requesting device
// itself, so every client sees the same set with its own row flagged.
func (s *Server) handlePresence(w http.ResponseWriter, r *http.Request) {
	rs, _ := sessionFrom(r.Context())
	devices, err := s.pairing.ListDevices()
	if err != nil {
		s.log.Error("list devices for presence", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not list online devices")
		return
	}
	online := make([]presenceDevice, 0, len(devices))
	for _, dev := range devices {
		if s.presence == nil || !s.presence.deviceOnline(dev.ID) {
			continue
		}
		online = append(online, presenceDevice{
			ID:      dev.ID,
			Name:    dev.Name,
			Current: dev.ID == rs.DeviceID,
		})
	}
	// Same name, different device: never merged, ordered by name then id.
	sort.Slice(online, func(i, j int) bool {
		if online[i].Name != online[j].Name {
			return online[i].Name < online[j].Name
		}
		return online[i].ID < online[j].ID
	})
	writeJSON(w, http.StatusOK, presenceResponse{Online: online})
}
