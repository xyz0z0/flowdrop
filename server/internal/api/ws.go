package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// handleEvents upgrades to WebSocket. Auth uses the same session cookie and the
// Origin check is explicit (cookies are sent cross-site for WS, unlike fetch).
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "login required")
		return
	}
	if _, err := s.sess.LookupSession(c.Value); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "session expired, login required")
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

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errCh:
			if !errors.Is(err, context.Canceled) && websocket.CloseStatus(err) == -1 {
				s.log.Debug("ws read ended", "err", err)
			}
			return
		case ev, ok := <-ch:
			if !ok {
				conn.Close(websocket.StatusGoingAway, "server shutting down")
				return
			}
			if err := wsjson.Write(ctx, conn, ev); err != nil {
				return
			}
		case <-keepAlive.C:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}
