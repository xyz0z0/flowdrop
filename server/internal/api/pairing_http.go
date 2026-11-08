package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"flowdrop/internal/auth"
	"flowdrop/internal/pairing"
)

// HubInfo is the non-secret Hub identity that clients compare before sending a
// device token. Endpoints only ever contains addresses the Hub actually bound.
type HubInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Endpoints []HubEndpointInfo `json:"endpoints"`
}

// HubEndpointInfo is one reachable address of the Hub.
type HubEndpointInfo struct {
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

const defaultHubName = "FlowDrop"

// hub resolves the reported Hub identity. Without a callback it reports the
// persistent ID and no endpoints: an address that has not been bound is never
// advertised.
func (s *Server) hub() HubInfo {
	if s.hubInfo != nil {
		h := s.hubInfo()
		if strings.TrimSpace(h.Name) == "" {
			h.Name = defaultHubName
		}
		if h.Endpoints == nil {
			h.Endpoints = []HubEndpointInfo{}
		}
		return h
	}
	return HubInfo{ID: s.hubID(), Name: defaultHubName, Endpoints: []HubEndpointInfo{}}
}

// sessionTTL is the browser cookie lifetime; device tokens themselves stay
// valid until revoked.
func (s *Server) sessionTTL() time.Duration { return s.cfg.SessionTTL() }

// sessionFor builds the request identity carrier. The old password session
// store is not consulted.
func (s *Server) sessionFor(dev pairing.Device, token string, now time.Time) reqSession {
	expires := now.Add(s.sessionTTL())
	return reqSession{
		DeviceID: dev.ID,
		Token:    token,
		Session: auth.Session{
			ID:         sessionIDForToken(token),
			UserID:     dev.ID,
			CreatedAt:  time.UnixMilli(dev.CreatedAt),
			LastSeenAt: time.UnixMilli(dev.LastSeenAt),
			ExpiresAt:  expires,
		},
	}
}

// sessionIDForToken hashes the token so the rate limiter never holds a raw
// credential.
func sessionIDForToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// bearerToken returns the Authorization Bearer token, if any. Tokens are only
// ever read from headers or cookies, never from the query string.
func bearerToken(r *http.Request) string {
	scheme, value, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

// authenticate resolves the request to a paired device via the session cookie
// or a Bearer device token. It returns the raw token alongside the identity so
// the caller can keep revalidating an open WebSocket.
func (s *Server) authenticate(r *http.Request) (reqSession, string, bool) {
	if s.pairing == nil {
		return reqSession{}, "", false
	}
	now := s.now()
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if dev, err := s.pairing.LookupToken(c.Value, now); err == nil {
			return s.sessionFor(dev, c.Value, now), c.Value, true
		}
	}
	if tok := bearerToken(r); tok != "" {
		if dev, err := s.pairing.LookupToken(tok, now); err == nil {
			return s.sessionFor(dev, tok, now), tok, true
		}
	}
	return reqSession{}, "", false
}

// tokenValid reports whether a device token is still usable. It is called
// before every WebSocket event and on a short interval, so a revoked device
// stops receiving events.
func (s *Server) tokenValid(token string) bool {
	if s.pairing == nil || token == "" {
		return false
	}
	_, err := s.pairing.LookupToken(token, s.now())
	return err == nil
}

type pairRequest struct {
	Code       string `json:"code"`
	DeviceName string `json:"device_name"`
	Native     bool   `json:"native"`
}

// handlePair consumes a locally generated pairing code and registers a device.
// The code is never logged, and error responses never distinguish why a code
// was rejected beyond the frozen pairing_invalid / pairing_locked codes.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	if s.pairing == nil {
		writeError(w, http.StatusServiceUnavailable, "pairing_unavailable", "pairing is not configured")
		return
	}
	// Bounded per source IP so pairing cannot be used as a query oracle; the
	// Hub-wide 5-attempt limit still lives in the pairing store.
	if !s.limiter.allow("pair:" + s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		return
	}

	var req pairRequest
	if err := s.readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	// A missing device name is a malformed request: it must not consume the
	// pairing code nor count as a failed attempt.
	if strings.TrimSpace(req.DeviceName) == "" {
		writeError(w, http.StatusBadRequest, "device_name_required", "device_name must not be empty")
		return
	}

	now := s.now()
	token, dev, err := s.pairing.Pair(req.Code, req.DeviceName, now)
	if err != nil {
		switch {
		case errors.Is(err, pairing.ErrLocked):
			writeError(w, http.StatusTooManyRequests, "pairing_locked",
				"too many wrong codes; generate a new code on the Hub")
		case errors.Is(err, pairing.ErrInvalidCode):
			writeError(w, http.StatusUnauthorized, "pairing_invalid", "pairing code is not valid")
		default:
			// Never surface internal details or the submitted code.
			s.log.Error("pair device", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "could not pair device")
		}
		return
	}

	csrf := auth.NewRandomToken(32)
	expires := now.Add(s.sessionTTL())
	s.setSessionCookies(w, r, token, csrf, expires)

	hub := s.hub()
	resp := sessionResponse{
		Authenticated: true,
		CSRFToken:     csrf,
		ExpiresAt:     expires.UnixMilli(),
		Limits:        s.limits(),
		DeviceID:      dev.ID,
		Hub:           &hub,
	}
	if req.Native {
		resp.DeviceToken = token
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSessionInfo stays unauthenticated so a client can decide whether it is
// paired. With a Bearer device token it restores the origin cookies and CSRF
// value; the token itself is never returned.
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	rs, token, ok := s.authenticate(r)
	if !ok {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			s.clearSessionCookies(w, r)
		}
		writeJSON(w, http.StatusOK, sessionResponse{Authenticated: false, Limits: s.limits()})
		return
	}

	csrf := ""
	if cc, err := r.Cookie(csrfCookie); err == nil {
		csrf = cc.Value
	}
	// Reissue cookies when the CSRF cookie is gone or the client authenticated
	// with a Bearer token, so it can use the normal double-submit flow.
	bearer := bearerToken(r) != ""
	if csrf == "" || bearer {
		if csrf == "" {
			csrf = auth.NewRandomToken(32)
		}
		s.setSessionCookies(w, r, token, csrf, rs.Session.ExpiresAt)
	}

	hub := s.hub()
	writeJSON(w, http.StatusOK, sessionResponse{
		Authenticated: true,
		CSRFToken:     csrf,
		ExpiresAt:     rs.Session.ExpiresAt.UnixMilli(),
		Limits:        s.limits(),
		DeviceID:      rs.DeviceID,
		Hub:           &hub,
	})
}

// handleLogout revokes the current device, which also ends its open WebSocket,
// and clears the cookies. The device record is removed on the Hub, so the
// saved device token cannot be reused.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	rs, _ := sessionFrom(r.Context())
	if err := s.RevokeDevice(rs.DeviceID); err != nil {
		if !errors.Is(err, pairing.ErrUnauthorized) {
			s.log.Error("revoke device on logout", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "could not revoke device")
			return
		}
	}
	s.clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// RevokeDevice revokes a paired device and stops its WebSocket. It is exported
// for the desktop's local management bridge; remote callers have no route to
// it beyond logging out their own device.
func (s *Server) RevokeDevice(id string) error {
	if s.pairing == nil {
		return pairing.ErrUnauthorized
	}
	err := s.pairing.RevokeDevice(id)
	// Open WebSockets revalidate their token before every event and once per
	// second, so a revoked device stops receiving events even when the
	// revocation came from a separate CLI process.
	if err != nil && !errors.Is(err, pairing.ErrUnauthorized) {
		return err
	}
	return err
}
