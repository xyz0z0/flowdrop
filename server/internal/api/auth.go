package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"flowdrop/internal/auth"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	CSRFToken     string `json:"csrf_token,omitempty"`
	Username      string `json:"username,omitempty"`
	ExpiresAt     int64  `json:"expires_at,omitempty"`
	// Limits lets the SPA pre-check uploads instead of hardcoding the default.
	Limits *limitsInfo `json:"limits,omitempty"`
}

type limitsInfo struct {
	MaxUploadBytes int64 `json:"max_upload_bytes"`
}

func (s *Server) limits() *limitsInfo {
	return &limitsInfo{MaxUploadBytes: s.cfg.MaxUploadBytes}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"service": "flowdrop-hub",
		"version": Version,
		"time":    s.now().UTC().Format(time.RFC3339),
	})
}

// handleSessionInfo is unauthenticated so the SPA can decide whether to show
// the login form. It never leaks item data.
func (s *Server) handleSessionInfo(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(sessionCookie)
	if err == nil && c.Value != "" {
		if sess, err := s.sess.LookupSession(c.Value); err == nil {
			csrf := ""
			if cc, err := r.Cookie(csrfCookie); err == nil {
				csrf = cc.Value
			}
			if csrf == "" {
				// Cookie was cleared by the browser but the session survived.
				csrf = auth.NewRandomToken(32)
				s.setSessionCookies(w, r, c.Value, csrf, sess.ExpiresAt)
			}
			writeJSON(w, http.StatusOK, sessionResponse{
				Authenticated: true,
				CSRFToken:     csrf,
				ExpiresAt:     sess.ExpiresAt.UnixMilli(),
				Limits:        s.limits(),
			})
			return
		}
		s.clearSessionCookies(w, r)
	}
	writeJSON(w, http.StatusOK, sessionResponse{Authenticated: false, Limits: s.limits()})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	ip := s.clientIP(r)
	if !s.loginLimiter.allow(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many login attempts, wait a few minutes")
		return
	}

	var req loginRequest
	if err := s.readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		req.Username = "admin"
	}
	if req.Password == "" || len(req.Password) > 1024 {
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		return
	}

	userID, err := s.sess.Authenticate(req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrNoUser):
			writeError(w, http.StatusForbidden, "not_initialized",
				"no admin password set; run 'flowdrop admin init' on the Hub")
		default:
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
		}
		return
	}

	token, sess, err := s.sess.CreateSession(userID, r.UserAgent())
	if err != nil {
		s.log.Error("create session", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "could not create session")
		return
	}
	csrf := auth.NewRandomToken(32)
	s.loginLimiter.reset(ip)
	s.setSessionCookies(w, r, token, csrf, sess.ExpiresAt)
	writeJSON(w, http.StatusOK, sessionResponse{
		Authenticated: true,
		CSRFToken:     csrf,
		Username:      req.Username,
		ExpiresAt:     sess.ExpiresAt.UnixMilli(),
		Limits:        s.limits(),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.originAllowed(r) {
		writeError(w, http.StatusForbidden, "origin_not_allowed", "request origin is not allowed")
		return
	}
	rs, _ := sessionFrom(r.Context())
	if err := s.sess.DeleteSession(rs.Token); err != nil {
		s.log.Error("delete session", "err", err)
	}
	s.clearSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}
