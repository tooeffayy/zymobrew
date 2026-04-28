package server

import (
	"encoding/json"
	"net/http"

	"zymobrew/internal/queries"
)

// handleGetVAPIDPublicKey returns the server's VAPID public key so browsers
// can create a PushSubscription. Returns 404 if web-push is not configured.
func (s *Server) handleGetVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.cfg.VAPIDPublicKey == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "web push not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"public_key": s.cfg.VAPIDPublicKey})
}

// handleSubscribePush registers a browser push subscription for the
// authenticated user. The browser provides the subscription object that
// includes the endpoint URL and encryption keys.
func (s *Server) handleSubscribePush(w http.ResponseWriter, r *http.Request) {
	if s.cfg.VAPIDPublicKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "web push not configured"})
		return
	}

	user, _ := userFromContext(r.Context())

	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "endpoint, keys.p256dh, and keys.auth are required"})
		return
	}

	dev, err := s.queries.UpsertPushDevice(r.Context(), queries.UpsertPushDeviceParams{
		UserID:   user.ID,
		Platform: "web",
		Token:    req.Endpoint,
		P256dh:   optText(req.Keys.P256dh),
		Auth:     optText(req.Keys.Auth),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"id": dev.ID.String()})
}

// handleUnsubscribePush removes a browser push subscription by endpoint URL.
func (s *Server) handleUnsubscribePush(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Endpoint == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "endpoint is required"})
		return
	}

	n, err := s.queries.DeletePushDevice(r.Context(), queries.DeletePushDeviceParams{
		UserID: user.ID,
		Token:  req.Endpoint,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "subscription not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
