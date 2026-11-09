package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// revocationCheckInterval bounds how long a revoked device can keep an already
// open event stream alive when the revocation came from another process (for
// example the local CLI). Events are additionally validated individually.
const revocationCheckInterval = time.Second

// handleEvents upgrades to WebSocket. Auth uses the paired-device token, either
// as the session cookie or as an Authorization Bearer header; the Origin check
// is explicit because cookies are sent cross-site for WS.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	rs, token, ok := s.authenticate(r)
	if !ok {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			s.clearSessionCookies(w, r)
		}
		writeError(w, http.StatusUnauthorized, "unauthenticated", "pairing required")
		return
	}
	if !s.limiter.allow(rs.TokenHash) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: false, // use our own explicit Origin check above
		OriginPatterns:     nil,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// If the device was revoked between the header check and the upgrade, do
	// not send anything at all.
	if !s.tokenValid(token) {
		conn.Close(websocket.StatusPolicyViolation, "unauthenticated")
		return
	}

	ch, unsubscribe := s.broker.Subscribe()
	defer unsubscribe()

	// Send an initial hello so clients know the socket is live and should
	// treat their REST snapshot as fresh.
	if err := wsjson.Write(ctx, conn, map[string]string{"type": "hello"}); err != nil {
		return
	}

	// Read loop: consume client pings/closes so the connection state is
	// accurate, and allow a client-initiated resync.
	errCh := make(chan error, 1)
	go func() {
		for {
			var msg map[string]string
			if err := wsjson.Read(ctx, conn, &msg); err != nil {
				errCh <- err
				return
			}
		}
	}()

	keepAlive := time.NewTicker(30 * time.Second)
	defer keepAlive.Stop()
	revocationCheck := time.NewTicker(revocationCheckInterval)
	defer revocationCheck.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
				s.log.Debug("ws read ended", "err", err)
			}
			return
		case <-revocationCheck.C:
			if !s.tokenValid(token) {
				conn.Close(websocket.StatusPolicyViolation, "unauthenticated")
				return
			}
		case ev, ok := <-ch:
			if !ok {
				conn.Close(websocket.StatusGoingAway, "server shutting down")
				return
			}
			// A revoked device must not receive further events even if the
			// revocation happened microseconds before this one.
			if !s.tokenValid(token) {
				conn.Close(websocket.StatusPolicyViolation, "unauthenticated")
				return
			}
			if err := wsjson.Write(ctx, conn, ev); err != nil {
				return
			}
		case <-keepAlive.C:
			if !s.tokenValid(token) {
				conn.Close(websocket.StatusPolicyViolation, "unauthenticated")
				return
			}
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
