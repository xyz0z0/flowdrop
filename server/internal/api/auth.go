package api

import (
	"net/http"
	"time"
)

// sessionResponse is the shared shape of the pairing and session endpoints.
// DeviceToken is only ever populated by a successful native pairing.
type sessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	CSRFToken     string `json:"csrf_token,omitempty"`
	ExpiresAt     int64  `json:"expires_at,omitempty"`
	// Limits lets the SPA pre-check uploads instead of hardcoding the default.
	Limits   *limitsInfo `json:"limits,omitempty"`
	DeviceID string      `json:"device_id,omitempty"`
	Hub      *HubInfo    `json:"hub,omitempty"`
	// DeviceToken is returned once, only when pairing asked for native=true.
	// A browser never receives it and it is never echoed by GET session.
	DeviceToken string `json:"device_token,omitempty"`
}

type limitsInfo struct {
	MaxUploadBytes int64 `json:"max_upload_bytes"`
}

func (s *Server) limits() *limitsInfo {
	return &limitsInfo{MaxUploadBytes: s.cfg.MaxUploadBytes}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{
		"status":  "ok",
		"service": "flowdrop-hub",
		"version": Version,
		"time":    s.now().UTC().Format(time.RFC3339),
	}
	// hub_id is the stable, non-secret Hub identity that unauthenticated LAN
	// probes compare before sending any credential.
	if id := s.hubID(); id != "" {
		body["hub_id"] = id
	}
	writeJSON(w, http.StatusOK, body)
}

// hubID returns the persistent Hub ID, or "" when pairing is not wired or the
// read failed. It never invents a value.
func (s *Server) hubID() string {
	if s.pairing == nil {
		return ""
	}
	id, err := s.pairing.HubID()
	if err != nil {
		return ""
	}
	return id
}
