package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"zymobrew/internal/queries"
)

// userPrefsView is the wire shape for /api/me/prefs. Mirrors the
// UserPrefs schema in openapi.yaml.
type userPrefsView struct {
	DegreeUnits string `json:"degree_units"`
	Timezone    string `json:"timezone"`
}

func toUserPrefsView(p queries.UserPref) userPrefsView {
	return userPrefsView{
		DegreeUnits: p.DegreeUnits,
		Timezone:    p.Timezone,
	}
}

// Defaults returned when the user has never saved prefs. Matches the
// DEFAULT clauses on the user_prefs table.
var defaultUserPrefs = userPrefsView{
	DegreeUnits: "C",
	Timezone:    "UTC",
}

func (s *Server) handleGetUserPrefs(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	p, err := s.queries.GetUserPrefs(r.Context(), user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, defaultUserPrefs)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, toUserPrefsView(p))
}

func (s *Server) handleUpdateUserPrefs(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	var req struct {
		DegreeUnits *string `json:"degree_units"`
		Timezone    *string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	params := queries.UpsertUserPrefsParams{UserID: user.ID}
	if req.DegreeUnits != nil {
		if *req.DegreeUnits != "C" && *req.DegreeUnits != "F" {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "degree_units must be 'C' or 'F'"})
			return
		}
		params.DegreeUnits = *req.DegreeUnits
	}
	if req.Timezone != nil {
		if _, err := time.LoadLocation(*req.Timezone); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid timezone"})
			return
		}
		params.Timezone = *req.Timezone
	}

	p, err := s.queries.UpsertUserPrefs(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, toUserPrefsView(p))
}
