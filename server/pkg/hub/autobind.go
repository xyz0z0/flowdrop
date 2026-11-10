// Auto binding: the Hub binds every eligible local IPv4 (RFC1918 LAN,
// Tailscale CGNAT and RFC1918 container/VM bridge addresses) on the primary
// listener's port, so a machine that moves between networks serves on its new
// address without a restart, and containers on the same host can reach the Hub.
// The address set is rescanned on an interval and listeners are added and dropped
// in place; one http.Server, handler, store, broker and auth stack serve all of
// them. Container bridges are served but never advertised over mDNS: a phone that
// resolved 172.17.0.1 could not reach it.
package hub

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"time"

	"flowdrop/internal/config"
	"flowdrop/internal/netbind"
)

// defaultAutoBindInterval is the rescan period when AutoBindInterval is unset.
const defaultAutoBindInterval = 5 * time.Second

// autoBindPort returns the port every auto-bound listener must share with the
// primary listener, or "" when auto binding cannot work. A configured port 0
// gets an arbitrary ephemeral port from the OS, and that port cannot be reused
// on other interfaces without a race, so auto binding stays disabled there.
func (h *Hub) autoBindPort() string {
	listens := h.cfg.Listens()
	if len(listens) == 0 {
		return ""
	}
	_, port, err := net.SplitHostPort(listens[0])
	if err != nil {
		return ""
	}
	if port == "" || port == "0" || !isNumeric(port) {
		return ""
	}
	return port
}

func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// startAutoBind binds the currently eligible addresses synchronously, so URLs()
// is complete when Start returns, and starts the rescan loop. Every step is best
// effort: a failure is logged and retried by the next rescan instead of failing
// Start, and the explicit additional_listen addresses keep their all-or-nothing
// semantics untouched.
func (h *Hub) startAutoBind(srv *http.Server, hubCtx context.Context) {
	listens := h.cfg.Listens()
	if len(listens) == 0 {
		return
	}
	port := h.autoBindPort()
	if port == "" {
		h.log.Warn("auto-bind disabled: the primary listener uses port 0 (an ephemeral port cannot be reused for other interfaces)",
			"listen", listens[0])
		return
	}

	wanted, kinds := h.autoBindTargets(port)
	added := h.addAutoListeners(srv, wanted)

	h.mu.Lock()
	h.autoOrder = wanted
	h.autoKinds = pickKinds(kinds, wanted)
	if len(added) > 0 {
		h.urls = h.rebuildURLsLocked()
	}
	hosts := h.boundHostsLocked()
	stopped := h.stopped
	if !stopped {
		h.autoBindDone = make(chan struct{})
	}
	done := h.autoBindDone
	h.mu.Unlock()
	if stopped {
		return
	}

	// The dynamic Host set is derived from the listeners that really exist, so a
	// client browsing http://<auto-bound-ip>:<port> passes hostCheck.
	h.api.SetBoundHosts(hosts)
	h.warnAboutAutoBound(added)

	h.log.Info("auto-bind enabled", "port", port, "interval", h.rescanInterval(), "targets", wanted)
	go h.autoBindLoop(srv, hubCtx, port, done)
}

// autoBindTargets scans the machine and returns the addresses to auto-bind on
// port, excluding every address this Hub already bound (the primary listener and
// explicit additional listeners) so a scan that reports the primary address can
// never cause EADDRINUSE. The second result records the netbind.Kind of every
// scanned address, which is what lets the mDNS filter tell a container bridge
// from an explicitly configured listener.
func (h *Hub) autoBindTargets(port string) (targets []string, kinds map[string]netbind.Kind) {
	addrs, err := h.scanAddrs()
	if err != nil {
		h.log.Warn("auto-bind scan failed", "err", err)
		return nil, nil
	}
	return h.filterUnbound(netbind.Targets(addrs, port)), scanKinds(addrs, port)
}

// scanKinds maps every scanned address to its kind, keyed by the "host:port"
// target auto binding uses for it.
func scanKinds(addrs []netbind.Addr, port string) map[string]netbind.Kind {
	if len(addrs) == 0 || port == "" {
		return nil
	}
	kinds := make(map[string]netbind.Kind, len(addrs))
	for _, a := range addrs {
		kinds[net.JoinHostPort(a.IP.String(), port)] = a.Kind
	}
	return kinds
}

// pickKinds keeps the kind entries for the addresses this Hub auto-binds.
// Addresses the operator configured explicitly (primary listener,
// additional_listen) are never in the map, so they keep their historical
// behaviour of always being advertised.
func pickKinds(kinds map[string]netbind.Kind, targets []string) map[string]netbind.Kind {
	if len(kinds) == 0 || len(targets) == 0 {
		return nil
	}
	out := make(map[string]netbind.Kind, len(targets))
	for _, target := range targets {
		if kind, ok := kinds[target]; ok {
			out[target] = kind
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// filterAdvertised drops container/virtual bridge listeners from the mDNS
// advertisement set: they are bound on purpose (LocalSend parity) but a phone
// that resolves them cannot reach the Hub. Explicitly configured listens keep
// the historical behaviour (the caller only passes auto-bound targets here).
func filterAdvertised(addrs []string, kinds map[string]netbind.Kind) []string {
	if len(addrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if kinds[addr] == netbind.KindVirtual {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// advertisedAddrs is the bound address set the Hub may publish over mDNS:
// everything it actually serves minus the auto-bound container bridges.
func (h *Hub) advertisedAddrs() []string {
	h.mu.Lock()
	addrs := h.boundAddrsLocked()
	kinds := h.autoKinds
	h.mu.Unlock()
	return filterAdvertised(addrs, kinds)
}

// filterUnbound drops targets that are already bound by one of h.listeners.
func (h *Hub) filterUnbound(targets []string) []string {
	if len(targets) == 0 {
		return nil
	}
	h.mu.Lock()
	bound := make(map[string]struct{}, len(h.listeners))
	for _, ln := range h.listeners {
		bound[ln.Addr().String()] = struct{}{}
	}
	h.mu.Unlock()
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		if _, ok := bound[target]; ok {
			continue
		}
		out = append(out, target)
	}
	return out
}

// scanAddrs reads the current address list from the configured source.
func (h *Hub) scanAddrs() ([]netbind.Addr, error) {
	if h.scan != nil {
		return h.scan()
	}
	return netbind.Scan()
}

// addAutoListeners binds and serves every target it can. A target that cannot be
// bound (address gone, no route) is logged and left to the next rescan; the Hub
// keeps serving. It returns the targets that are now bound.
func (h *Hub) addAutoListeners(srv *http.Server, targets []string) []string {
	added := make([]string, 0, len(targets))
	lns := make([]net.Listener, 0, len(targets))
	for _, addr := range targets {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			h.log.Warn("auto-bind skipped", "addr", addr, "err", err)
			continue
		}
		added = append(added, addr)
		lns = append(lns, ln)
	}
	if len(lns) == 0 {
		return nil
	}
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		for _, ln := range lns {
			_ = ln.Close()
		}
		return nil
	}
	for i, ln := range lns {
		h.autoListeners[added[i]] = ln
		h.listeners = append(h.listeners, ln)
	}
	h.mu.Unlock()
	for _, ln := range lns {
		go h.serveListener(srv, ln)
	}
	return added
}

// autoBindLoop rescans the address set until hubCtx is cancelled. Stop cancels
// the context and then waits for this goroutine, so the ticker never outlives
// the Hub.
func (h *Hub) autoBindLoop(srv *http.Server, hubCtx context.Context, port string, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(h.rescanInterval())
	defer ticker.Stop()
	for {
		select {
		case <-hubCtx.Done():
			return
		case <-ticker.C:
			if hubCtx.Err() != nil {
				return
			}
			h.autoBindRescan(srv, hubCtx, port)
		}
	}
}

func (h *Hub) rescanInterval() time.Duration {
	if h.autoBindInterval > 0 {
		return h.autoBindInterval
	}
	return defaultAutoBindInterval
}

// autoBindRescan applies one scan result: new addresses get a listener, vanished
// addresses lose theirs, and the URL list, dynamic Host set and mDNS record are
// refreshed when the set actually changed.
func (h *Hub) autoBindRescan(srv *http.Server, hubCtx context.Context, port string) {
	addrs, err := h.scanAddrs()
	if err != nil {
		h.log.Warn("auto-bind scan failed", "err", err)
		return
	}
	targets := netbind.Targets(addrs, port)
	kinds := scanKinds(addrs, port)

	// Addresses bound outside the auto set (primary + explicit additional) must
	// never be re-bound. Auto-bound ones are compared against the previous auto
	// set instead, so an unchanged address is not mistaken for an addition.
	h.mu.Lock()
	blocked := make(map[string]struct{}, len(h.listeners))
	for _, ln := range h.listeners {
		addr := ln.Addr().String()
		if _, isAuto := h.autoListeners[addr]; !isAuto {
			blocked[addr] = struct{}{}
		}
	}
	old := make([]string, 0, len(h.autoListeners))
	for addr := range h.autoListeners {
		old = append(old, addr)
	}
	stopped := h.stopped
	h.mu.Unlock()
	if stopped {
		return
	}

	wanted := make([]string, 0, len(targets))
	for _, target := range targets {
		if _, ok := blocked[target]; ok {
			continue
		}
		wanted = append(wanted, target)
	}
	addTargets, removed := netbind.Diff(old, wanted)
	if len(addTargets) == 0 && len(removed) == 0 {
		return
	}

	var closedLns []net.Listener
	if len(removed) > 0 {
		h.mu.Lock()
		for _, addr := range removed {
			ln, ok := h.autoListeners[addr]
			if !ok {
				continue
			}
			delete(h.autoListeners, addr)
			h.listeners = removeListener(h.listeners, ln)
			closedLns = append(closedLns, ln)
		}
		h.mu.Unlock()
		// Closing drops in-flight requests on that address; an address that left
		// the machine cannot serve anything new anyway.
		for _, ln := range closedLns {
			_ = ln.Close()
		}
	}
	added := h.addAutoListeners(srv, addTargets)
	if len(added) == 0 && len(closedLns) == 0 {
		return
	}

	h.mu.Lock()
	h.autoOrder = wanted
	h.autoKinds = pickKinds(kinds, wanted)
	h.urls = h.rebuildURLsLocked()
	urls := append([]string(nil), h.urls...)
	hosts := h.boundHostsLocked()
	boundAddrs := h.boundAddrsLocked()
	stopped = h.stopped
	disc := h.disc
	h.disc = nil
	h.mu.Unlock()
	if stopped {
		return
	}

	h.api.SetBoundHosts(hosts)
	h.warnAboutAutoBound(added)
	// Re-register mDNS for the new address set: withdraw the old record first so
	// clients never resolve an address this Hub just dropped.
	if disc != nil {
		disc.Close()
	}
	disc = startDiscovery(hubCtx, h.cfg, filterAdvertised(boundAddrs, pickKinds(kinds, wanted)), h.log)
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		if disc != nil {
			disc.Close()
		}
		return
	}
	h.disc = disc
	h.mu.Unlock()

	h.log.Info("auto-bind changed", "added", added, "removed", removed, "urls", urls)
}

// serveListener serves one bound listener on the shared http.Server. A listener
// that closed on request (Stop or a vanished address) is a normal shutdown, not
// a failure, and never stops the rest of the Hub.
func (h *Hub) serveListener(srv *http.Server, ln net.Listener) {
	addr := ln.Addr().String()
	err := srv.Serve(ln)
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return
	}
	h.mu.Lock()
	stopped := h.stopped
	owned := hasListener(h.listeners, ln)
	h.mu.Unlock()
	if !stopped && owned {
		h.log.Error("http server stopped unexpectedly", "listen", addr, "err", err)
	}
}

func hasListener(listeners []net.Listener, target net.Listener) bool {
	for _, ln := range listeners {
		if ln == target {
			return true
		}
	}
	return false
}

func removeListener(listeners []net.Listener, target net.Listener) []net.Listener {
	out := listeners[:0]
	for _, ln := range listeners {
		if ln != target {
			out = append(out, ln)
		}
	}
	return out
}

// rebuildURLsLocked recomputes the reported URL list: the primary listener
// first, then the live auto-bound listeners in auto-bind target order.
func (h *Hub) rebuildURLsLocked() []string {
	urls := make([]string, 0, 1+len(h.autoListeners))
	urls = append(urls, h.baseURL)
	seen := make(map[string]struct{}, len(h.autoListeners))
	for _, target := range h.autoOrder {
		ln, ok := h.autoListeners[target]
		if !ok {
			continue
		}
		urls = append(urls, listenerURL(ln))
		seen[target] = struct{}{}
	}
	if len(seen) == len(h.autoListeners) {
		return urls
	}
	// Defensive: an alive listener whose address is no longer in the scanned
	// order is reported after the ordered ones instead of being hidden.
	extra := make([]string, 0, len(h.autoListeners)-len(seen))
	for target := range h.autoListeners {
		if _, ok := seen[target]; !ok {
			extra = append(extra, target)
		}
	}
	sort.Strings(extra)
	for _, target := range extra {
		urls = append(urls, listenerURL(h.autoListeners[target]))
	}
	return urls
}

// boundAddrs snapshots the actual bound addresses, primary first.
func (h *Hub) boundAddrs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.boundAddrsLocked()
}

func (h *Hub) boundAddrsLocked() []string {
	addrs := make([]string, 0, len(h.listeners))
	for _, ln := range h.listeners {
		addrs = append(addrs, ln.Addr().String())
	}
	return addrs
}

// boundHostsLocked returns the concrete IP of every bound listener. Wildcard
// binds never appear here (config validation rejects them) and the API ignores
// them defensively.
func (h *Hub) boundHostsLocked() []string {
	hosts := make([]string, 0, len(h.listeners))
	for _, ln := range h.listeners {
		if host, _, err := net.SplitHostPort(ln.Addr().String()); err == nil && host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// warnAboutAutoBound keeps "which plaintext addresses did we bind" visible for
// the addresses auto binding added. Configured addresses keep their existing
// warnAboutNetworkMode output.
func (h *Hub) warnAboutAutoBound(addrs []string) {
	if len(addrs) == 0 || h.cfg.CookieSecure {
		return
	}
	plain := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		if !config.IsLoopbackListen(addr) {
			plain = append(plain, addr)
		}
	}
	if len(plain) == 0 {
		return
	}
	h.log.Warn("auto-bound plaintext HTTP address: session cookies and content are not encrypted on the wire",
		"listens", plain, "hint", "use only on a trusted network; prefer Tailscale or a TLS reverse proxy elsewhere")
}
