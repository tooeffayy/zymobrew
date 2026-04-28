package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/queries"
)

const (
	maxTemplateTitle = 200
	maxTemplateDesc  = 2 * 1024
)

// disallowedTemplateAnchors lists anchor values that are not meaningful on a
// recipe template (no wall-clock date to resolve against).
var disallowedTemplateAnchors = map[string]bool{
	"absolute": true,
}

// --- view type ------------------------------------------------------------

type reminderTemplateView struct {
	ID                 string  `json:"id"`
	RecipeID           string  `json:"recipe_id"`
	Title              string  `json:"title"`
	Description        string  `json:"description,omitempty"`
	Anchor             string  `json:"anchor"`
	OffsetMinutes      int32   `json:"offset_minutes"`
	SuggestedEventKind *string `json:"suggested_event_kind,omitempty"`
	SortOrder          int32   `json:"sort_order"`
}

func toTemplateView(t queries.RecipeReminderTemplate) reminderTemplateView {
	v := reminderTemplateView{
		ID:            t.ID.String(),
		RecipeID:      t.RecipeID.String(),
		Title:         t.Title,
		Description:   textOrEmpty(t.Description),
		Anchor:        string(t.Anchor),
		OffsetMinutes: t.OffsetMinutes,
		SortOrder:     t.SortOrder,
	}
	if t.SuggestedEventKind.Valid {
		s := string(t.SuggestedEventKind.EventKind)
		v.SuggestedEventKind = &s
	}
	return v
}

// --- helpers --------------------------------------------------------------

// recipeForTemplates loads a recipe and checks visibility. Returns nil + writes
// a 404 if the recipe is not accessible to the user (user may be nil for public).
func (s *Server) recipeForTemplates(w http.ResponseWriter, r *http.Request, id uuid.UUID) *queries.Recipe {
	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return nil
	}
	user, authed := userFromContext(r.Context())
	switch recipe.Visibility {
	case queries.VisibilityPrivate:
		if !authed || user.ID != recipe.AuthorID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return nil
		}
	}
	return &recipe
}

// --- handlers -------------------------------------------------------------

func (s *Server) handleListReminderTemplates(w http.ResponseWriter, r *http.Request) {
	recipeID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recipe id"})
		return
	}

	if s.recipeForTemplates(w, r, recipeID) == nil {
		return
	}

	templates, err := s.queries.ListReminderTemplates(r.Context(), recipeID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	views := make([]reminderTemplateView, 0, len(templates))
	for _, t := range templates {
		views = append(views, toTemplateView(t))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleCreateReminderTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	recipeID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recipe id"})
		return
	}

	recipe := s.recipeForTemplates(w, r, recipeID)
	if recipe == nil {
		return
	}
	if recipe.AuthorID != user.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var req struct {
		Title              string  `json:"title"`
		Description        string  `json:"description"`
		Anchor             string  `json:"anchor"`
		OffsetMinutes      int32   `json:"offset_minutes"`
		SuggestedEventKind *string `json:"suggested_event_kind"`
		SortOrder          int32   `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if req.Title == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "title is required"})
		return
	}
	if len(req.Title) > maxTemplateTitle {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "title too long"})
		return
	}
	if len(req.Description) > maxTemplateDesc {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "description too long"})
		return
	}
	if req.Anchor == "" {
		req.Anchor = "pitch"
	}
	if disallowedTemplateAnchors[req.Anchor] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "anchor 'absolute' is not valid on recipe templates"})
		return
	}

	var sek queries.NullEventKind
	if req.SuggestedEventKind != nil {
		sek = queries.NullEventKind{EventKind: queries.EventKind(*req.SuggestedEventKind), Valid: true}
	}

	tmpl, err := s.queries.CreateReminderTemplate(r.Context(), queries.CreateReminderTemplateParams{
		RecipeID:           recipeID,
		Title:              req.Title,
		Description:        pgtype.Text{String: req.Description, Valid: req.Description != ""},
		Anchor:             queries.ReminderAnchor(req.Anchor),
		OffsetMinutes:      req.OffsetMinutes,
		SuggestedEventKind: sek,
		SortOrder:          req.SortOrder,
	})
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid anchor or suggested_event_kind value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusCreated, toTemplateView(tmpl))
}

func (s *Server) handleUpdateReminderTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	recipeID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recipe id"})
		return
	}
	templateID, err := uuid.Parse(chi.URLParam(r, "templateId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid template id"})
		return
	}

	recipe := s.recipeForTemplates(w, r, recipeID)
	if recipe == nil {
		return
	}
	if recipe.AuthorID != user.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	var req struct {
		Title              *string `json:"title"`
		Description        *string `json:"description"`
		Anchor             *string `json:"anchor"`
		OffsetMinutes      *int32  `json:"offset_minutes"`
		SuggestedEventKind *string `json:"suggested_event_kind"`
		SortOrder          *int32  `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if req.Title != nil && len(*req.Title) > maxTemplateTitle {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "title too long"})
		return
	}
	if req.Description != nil && len(*req.Description) > maxTemplateDesc {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "description too long"})
		return
	}
	if req.Anchor != nil && disallowedTemplateAnchors[*req.Anchor] {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "anchor 'absolute' is not valid on recipe templates"})
		return
	}

	params := queries.UpdateReminderTemplateParams{
		ID:       templateID,
		RecipeID: recipeID,
	}
	if req.Title != nil {
		params.Title = pgtype.Text{String: *req.Title, Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	if req.Anchor != nil {
		params.Anchor = queries.NullReminderAnchor{ReminderAnchor: queries.ReminderAnchor(*req.Anchor), Valid: true}
	}
	if req.OffsetMinutes != nil {
		params.OffsetMinutes = pgtype.Int4{Int32: *req.OffsetMinutes, Valid: true}
	}
	if req.SuggestedEventKind != nil {
		params.SuggestedEventKind = queries.NullEventKind{EventKind: queries.EventKind(*req.SuggestedEventKind), Valid: true}
	}
	if req.SortOrder != nil {
		params.SortOrder = pgtype.Int4{Int32: *req.SortOrder, Valid: true}
	}

	tmpl, err := s.queries.UpdateReminderTemplate(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid anchor or suggested_event_kind value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, toTemplateView(tmpl))
}

func (s *Server) handleDeleteReminderTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	recipeID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recipe id"})
		return
	}
	templateID, err := uuid.Parse(chi.URLParam(r, "templateId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid template id"})
		return
	}

	recipe := s.recipeForTemplates(w, r, recipeID)
	if recipe == nil {
		return
	}
	if recipe.AuthorID != user.ID {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	n, err := s.queries.DeleteReminderTemplate(r.Context(), queries.DeleteReminderTemplateParams{
		ID:       templateID,
		RecipeID: recipeID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
