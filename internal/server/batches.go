package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/queries"
)

const (
	defaultBrewType = "mead"
	maxBatchName    = 200
	maxNotesBytes   = 10 * 1024
)

// allowedBrewTypes is the API-surface allow-list. The brew_type ENUM in
// Postgres covers everything (mead/cider/wine/beer/kombucha) so the schema
// supports later phases without a migration; beer + kombucha need new flow
// logic (mash/boil for beer, F1/F2 for kombucha) and are gated here until
// Phases 6-7 ship.
var allowedBrewTypes = map[string]struct{}{
	"mead":  {},
	"cider": {},
	"wine":  {},
}

const unsupportedBrewTypeMsg = "brew_type must be one of: mead, cider, wine"

// --- DTOs ------------------------------------------------------------------

type batchView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	BrewType   string     `json:"brew_type"`
	Stage      string     `json:"stage"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	BottledAt  *time.Time `json:"bottled_at,omitempty"`
	Visibility string     `json:"visibility"`
	Notes      string     `json:"notes,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func toBatchView(b queries.Batch) batchView {
	return batchView{
		ID:         b.ID.String(),
		Name:       b.Name,
		BrewType:   string(b.BrewType),
		Stage:      string(b.Stage),
		StartedAt:  tsPtr(b.StartedAt),
		BottledAt:  tsPtr(b.BottledAt),
		Visibility: string(b.Visibility),
		Notes:      textOrEmpty(b.Notes),
		CreatedAt:  b.CreatedAt.Time,
		UpdatedAt:  b.UpdatedAt.Time,
	}
}

type readingView struct {
	ID           string    `json:"id"`
	BatchID      string    `json:"batch_id"`
	TakenAt      time.Time `json:"taken_at"`
	Gravity      *float64  `json:"gravity,omitempty"`
	TemperatureC *float64  `json:"temperature_c,omitempty"`
	PH           *float64  `json:"ph,omitempty"`
	Notes        string    `json:"notes,omitempty"`
	Source       string    `json:"source"`
}

func toReadingView(r queries.Reading) readingView {
	return readingView{
		ID:           r.ID.String(),
		BatchID:      r.BatchID.String(),
		TakenAt:      r.TakenAt.Time,
		Gravity:      numericPtr(r.Gravity),
		TemperatureC: numericPtr(r.TemperatureC),
		PH:           numericPtr(r.Ph),
		Notes:        textOrEmpty(r.Notes),
		Source:       r.Source,
	}
}

// --- conversion helpers ----------------------------------------------------

func tsPtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func textOrEmpty(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

// numericPtr converts a nullable pgtype.Numeric to *float64. Brewing
// precision (gravity 1.xxx, temp °C, pH) is well within float64.
func numericPtr(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	v := f.Float64
	return &v
}

// floatToNumeric converts a *float64 to pgtype.Numeric for inserts.
func floatToNumeric(p *float64) pgtype.Numeric {
	if p == nil {
		return pgtype.Numeric{}
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(*p, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}
	}
	return n
}

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, name))
}

func optText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func optTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// --- batch handlers --------------------------------------------------------

type createBatchRequest struct {
	Name       string     `json:"name"`
	BrewType   string     `json:"brew_type"`
	Stage      string     `json:"stage"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	Notes      string     `json:"notes,omitempty"`
	Visibility string     `json:"visibility"`
	RecipeID   *string    `json:"recipe_id,omitempty"`
}

func (s *Server) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	var req createBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Name == "" || len(req.Name) > maxBatchName {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required (max 200 chars)"})
		return
	}
	if len(req.Notes) > maxNotesBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notes too long"})
		return
	}
	if req.BrewType == "" {
		req.BrewType = defaultBrewType
	}
	if _, ok := allowedBrewTypes[req.BrewType]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": unsupportedBrewTypeMsg})
		return
	}
	if req.Stage == "" {
		req.Stage = "planning"
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}

	params := queries.CreateBatchParams{
		BrewerID:   user.ID,
		Name:       req.Name,
		BrewType:   queries.BrewType(req.BrewType),
		Stage:      queries.BatchStage(req.Stage),
		StartedAt:  optTime(req.StartedAt),
		Notes:      optText(req.Notes),
		Visibility: queries.Visibility(req.Visibility),
	}

	// If a recipe is provided, pin the batch to its current revision.
	if req.RecipeID != nil {
		rid, err := uuid.Parse(*req.RecipeID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recipe_id"})
			return
		}
		recipe, err := s.queries.GetRecipeByID(r.Context(), rid)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "recipe not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
			return
		}
		// private recipes can only be used by their owner
		if recipe.Visibility == queries.VisibilityPrivate && recipe.AuthorID != user.ID {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "recipe not found"})
			return
		}
		params.RecipeID = uuid.NullUUID{UUID: recipe.ID, Valid: true}
		params.RecipeRevisionID = recipe.CurrentRevisionID
	}

	batch, err := s.queries.CreateBatch(r.Context(), params)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid stage or visibility value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	// Materialize batch_start-anchored templates if the batch starts immediately.
	if batch.StartedAt.Valid && batch.RecipeID.Valid {
		s.materializeTemplates(r.Context(), batch, queries.ReminderAnchorBatchStart, batch.StartedAt.Time)
	}

	writeJSON(w, http.StatusCreated, toBatchView(batch))
}

func (s *Server) handleListBatches(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	p, ok := parseListPagination(w, r)
	if !ok {
		return
	}

	rows, err := s.queries.ListBatchesForUser(r.Context(), queries.ListBatchesForUserParams{
		BrewerID: user.ID,
		CursorTs: p.CursorTs,
		CursorID: p.CursorID,
		LimitN:   p.Limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]batchView, 0, len(rows))
	for _, b := range rows {
		views = append(views, toBatchView(b))
	}
	next := nextCursor(len(rows), p.Limit, func() (time.Time, uuid.UUID) {
		last := rows[len(rows)-1]
		// Sort key is COALESCE(started_at, created_at) — pick whichever
		// the SQL did, so the cursor matches the row comparison.
		ts := last.CreatedAt.Time
		if last.StartedAt.Valid {
			ts = last.StartedAt.Time
		}
		return ts, last.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"batches": views, "next_cursor": nullableCursor(next)})
}

func (s *Server) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	batch, err := s.queries.GetBatchForUser(r.Context(), queries.GetBatchForUserParams{
		ID: id, BrewerID: user.ID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	writeJSON(w, http.StatusOK, toBatchView(batch))
}

type updateBatchRequest struct {
	Name       *string    `json:"name,omitempty"`
	Stage      *string    `json:"stage,omitempty"`
	Notes      *string    `json:"notes,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	BottledAt  *time.Time `json:"bottled_at,omitempty"`
	Visibility *string    `json:"visibility,omitempty"`
}

func (s *Server) handleUpdateBatch(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var req updateBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Name != nil && (*req.Name == "" || len(*req.Name) > maxBatchName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if req.Notes != nil && len(*req.Notes) > maxNotesBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "notes too long"})
		return
	}

	params := queries.UpdateBatchParams{ID: id, BrewerID: user.ID}
	if req.Name != nil {
		params.Name = pgtype.Text{String: *req.Name, Valid: true}
	}
	if req.Stage != nil {
		params.Stage = queries.NullBatchStage{BatchStage: queries.BatchStage(*req.Stage), Valid: true}
	}
	if req.Notes != nil {
		params.Notes = pgtype.Text{String: *req.Notes, Valid: true}
	}
	if req.StartedAt != nil {
		params.StartedAt = pgtype.Timestamptz{Time: *req.StartedAt, Valid: true}
	}
	if req.BottledAt != nil {
		params.BottledAt = pgtype.Timestamptz{Time: *req.BottledAt, Valid: true}
	}
	if req.Visibility != nil {
		params.Visibility = queries.NullVisibility{Visibility: queries.Visibility(*req.Visibility), Valid: true}
	}

	batch, err := s.queries.UpdateBatch(r.Context(), params)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case isInvalidTextRepresentation(err):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid stage or visibility value"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		}
		return
	}

	// Re-run materialization whenever started_at is included in the patch.
	// On first set this creates the reminder rows; on re-anchor it shifts
	// fire_at on existing scheduled reminders. See materializeTemplates.
	if req.StartedAt != nil && batch.StartedAt.Valid && batch.RecipeID.Valid {
		s.materializeTemplates(r.Context(), batch, queries.ReminderAnchorBatchStart, batch.StartedAt.Time)
	}

	writeJSON(w, http.StatusOK, toBatchView(batch))
}

func (s *Server) handleDeleteBatch(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	rows, err := s.queries.DeleteBatch(r.Context(), queries.DeleteBatchParams{
		ID: id, BrewerID: user.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if rows == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- reading handlers ------------------------------------------------------

type createReadingRequest struct {
	TakenAt      *time.Time `json:"taken_at,omitempty"`
	Gravity      *float64   `json:"gravity,omitempty"`
	TemperatureC *float64   `json:"temperature_c,omitempty"`
	PH           *float64   `json:"ph,omitempty"`
	Notes        string     `json:"notes,omitempty"`
	Source       string     `json:"source,omitempty"`
}

func (s *Server) handleCreateReading(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	if !s.userOwnsBatch(r.Context(), user.ID, batchID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req createReadingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Gravity == nil && req.TemperatureC == nil && req.PH == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one of gravity, temperature_c, ph required"})
		return
	}
	taken := time.Now()
	if req.TakenAt != nil {
		taken = *req.TakenAt
	}
	source := req.Source
	if source == "" {
		source = "manual"
	}

	reading, err := s.queries.CreateReading(r.Context(), queries.CreateReadingParams{
		BatchID:      batchID,
		TakenAt:      pgtype.Timestamptz{Time: taken, Valid: true},
		Gravity:      floatToNumeric(req.Gravity),
		TemperatureC: floatToNumeric(req.TemperatureC),
		Ph:           floatToNumeric(req.PH),
		Notes:        optText(req.Notes),
		Source:       source,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, toReadingView(reading))
}

func (s *Server) handleListReadings(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	if !s.userOwnsBatch(r.Context(), user.ID, batchID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	rows, err := s.queries.ListReadingsForBatch(r.Context(), batchID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]readingView, 0, len(rows))
	for _, rd := range rows {
		views = append(views, toReadingView(rd))
	}
	writeJSON(w, http.StatusOK, map[string]any{"readings": views})
}

// userOwnsBatch returns true iff the batch exists and is owned by the user.
// One small extra query, but it keeps reading/event handlers from leaking
// existence of other users' batches via timing or 403 vs 404.
func (s *Server) userOwnsBatch(ctx context.Context, userID, batchID uuid.UUID) bool {
	_, err := s.queries.GetBatchForUser(ctx, queries.GetBatchForUserParams{
		ID: batchID, BrewerID: userID,
	})
	return err == nil
}

// materializeTemplates makes the reminders for this batch+anchor reflect
// `anchorTime`. First it shifts fire_at on already-materialized scheduled
// reminders (re-anchor: batch.started_at being patched, or eventually a
// pitch/rack/bottle event's occurred_at being edited). Then it creates rows
// for any templates not yet materialized — covers initial materialization
// and templates added after the batch started.
//
// Both writes are best-effort and do not fail the triggering request. They
// don't share a transaction; if the second one fails after the first
// succeeded, the next call (via another PATCH or event) will reconcile.
func (s *Server) materializeTemplates(ctx context.Context, batch queries.Batch, anchor queries.ReminderAnchor, anchorTime time.Time) {
	if !batch.RecipeID.Valid {
		return
	}
	_ = s.queries.ReanchorReminders(ctx, queries.ReanchorRemindersParams{
		BatchID:    batch.ID,
		RecipeID:   batch.RecipeID.UUID,
		Anchor:     anchor,
		AnchorTime: pgtype.Timestamptz{Time: anchorTime, Valid: true},
	})
	_ = s.queries.MaterializeReminderTemplates(ctx, queries.MaterializeReminderTemplatesParams{
		BatchID:    batch.ID,
		UserID:     batch.BrewerID,
		RecipeID:   batch.RecipeID.UUID,
		Anchor:     anchor,
		AnchorTime: pgtype.Timestamptz{Time: anchorTime, Valid: true},
	})
}

// --- event handlers -------------------------------------------------------

type createEventRequest struct {
	OccurredAt  *time.Time      `json:"occurred_at,omitempty"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
}

type eventView struct {
	ID          string          `json:"id"`
	BatchID     string          `json:"batch_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Details     json.RawMessage `json:"details"`
}

func toEventView(e queries.BatchEvent) eventView {
	details := json.RawMessage(e.Details)
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	return eventView{
		ID:          e.ID.String(),
		BatchID:     e.BatchID.String(),
		OccurredAt:  e.OccurredAt.Time,
		Kind:        string(e.Kind),
		Title:       textOrEmpty(e.Title),
		Description: textOrEmpty(e.Description),
		Details:     details,
	}
}

func (s *Server) handleCreateBatchEvent(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	batch, err := s.queries.GetBatchForUser(r.Context(), queries.GetBatchForUserParams{
		ID: batchID, BrewerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var req createEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Kind == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind required"})
		return
	}

	occurred := time.Now()
	if req.OccurredAt != nil {
		occurred = *req.OccurredAt
	}
	details := []byte("{}")
	if len(req.Details) > 0 {
		// Reject anything that isn't a JSON object — the column has DEFAULT '{}'
		// and downstream code assumes object shape.
		if !json.Valid(req.Details) || req.Details[0] != '{' {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "details must be a JSON object"})
			return
		}
		details = req.Details
	}

	event, err := s.queries.CreateBatchEvent(r.Context(), queries.CreateBatchEventParams{
		BatchID:     batchID,
		OccurredAt:  pgtype.Timestamptz{Time: occurred, Valid: true},
		Kind:        queries.EventKind(req.Kind),
		Title:       optText(req.Title),
		Description: optText(req.Description),
		Details:     details,
	})
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid kind value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	// Materialize reminder templates anchored to this event kind.
	anchorForKind := map[queries.EventKind]queries.ReminderAnchor{
		queries.EventKindPitch:  queries.ReminderAnchorPitch,
		queries.EventKindRack:   queries.ReminderAnchorRack,
		queries.EventKindBottle: queries.ReminderAnchorBottle,
	}
	if anchor, ok := anchorForKind[event.Kind]; ok && batch.RecipeID.Valid {
		s.materializeTemplates(r.Context(), batch, anchor, event.OccurredAt.Time)
	}

	writeJSON(w, http.StatusCreated, toEventView(event))
}

func (s *Server) handleListBatchEvents(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	if !s.userOwnsBatch(r.Context(), user.ID, batchID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	rows, err := s.queries.ListBatchEventsForBatch(r.Context(), batchID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]eventView, 0, len(rows))
	for _, e := range rows {
		views = append(views, toEventView(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": views})
}

// updateEventRequest uses pointer fields so omitted vs explicitly-set
// can be distinguished — COALESCE in the SQL leaves omitted columns
// alone. Title/Description set to empty string clears the column
// (we send an empty pgtype.Text with Valid=true).
type updateEventRequest struct {
	OccurredAt  *time.Time       `json:"occurred_at,omitempty"`
	Kind        *string          `json:"kind,omitempty"`
	Title       *string          `json:"title,omitempty"`
	Description *string          `json:"description,omitempty"`
	Details     *json.RawMessage `json:"details,omitempty"`
}

func (s *Server) handleUpdateBatchEvent(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	eventID, err := uuid.Parse(chi.URLParam(r, "eventId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event id"})
		return
	}
	batch, err := s.queries.GetBatchForUser(r.Context(), queries.GetBatchForUserParams{
		ID: batchID, BrewerID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var req updateEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	params := queries.UpdateBatchEventParams{
		ID:      eventID,
		BatchID: batchID,
	}
	if req.OccurredAt != nil {
		params.OccurredAt = pgtype.Timestamptz{Time: *req.OccurredAt, Valid: true}
	}
	if req.Kind != nil {
		params.Kind = queries.NullEventKind{EventKind: queries.EventKind(*req.Kind), Valid: true}
	}
	if req.Title != nil {
		params.Title = pgtype.Text{String: *req.Title, Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	if req.Details != nil {
		raw := *req.Details
		if len(raw) > 0 && (!json.Valid(raw) || raw[0] != '{') {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "details must be a JSON object"})
			return
		}
		params.Details = raw
	}

	event, err := s.queries.UpdateBatchEvent(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid kind value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	// If the (possibly updated) kind anchors reminders, re-materialize
	// against the (possibly updated) occurred_at — same logic as create.
	// Re-anchor handles the case where the user shifted the time on an
	// existing pitch/rack/bottle event.
	anchorForKind := map[queries.EventKind]queries.ReminderAnchor{
		queries.EventKindPitch:  queries.ReminderAnchorPitch,
		queries.EventKindRack:   queries.ReminderAnchorRack,
		queries.EventKindBottle: queries.ReminderAnchorBottle,
	}
	if anchor, ok := anchorForKind[event.Kind]; ok && batch.RecipeID.Valid {
		s.materializeTemplates(r.Context(), batch, anchor, event.OccurredAt.Time)
	}

	writeJSON(w, http.StatusOK, toEventView(event))
}

func (s *Server) handleDeleteBatchEvent(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	eventID, err := uuid.Parse(chi.URLParam(r, "eventId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid event id"})
		return
	}
	if !s.userOwnsBatch(r.Context(), user.ID, batchID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	n, err := s.queries.DeleteBatchEvent(r.Context(), queries.DeleteBatchEventParams{
		ID:      eventID,
		BatchID: batchID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- tasting note handlers ------------------------------------------------

type createTastingNoteRequest struct {
	TastedAt  *time.Time `json:"tasted_at,omitempty"`
	Rating    *int       `json:"rating,omitempty"`
	Aroma     string     `json:"aroma,omitempty"`
	Flavor    string     `json:"flavor,omitempty"`
	Mouthfeel string     `json:"mouthfeel,omitempty"`
	Finish    string     `json:"finish,omitempty"`
	Notes     string     `json:"notes,omitempty"`
}

type tastingNoteView struct {
	ID        string    `json:"id"`
	BatchID   string    `json:"batch_id"`
	AuthorID  string    `json:"author_id"`
	TastedAt  time.Time `json:"tasted_at"`
	Rating    *int      `json:"rating,omitempty"`
	Aroma     string    `json:"aroma,omitempty"`
	Flavor    string    `json:"flavor,omitempty"`
	Mouthfeel string    `json:"mouthfeel,omitempty"`
	Finish    string    `json:"finish,omitempty"`
	Notes     string    `json:"notes,omitempty"`
}

func toTastingNoteView(n queries.TastingNote) tastingNoteView {
	return tastingNoteView{
		ID:        n.ID.String(),
		BatchID:   n.BatchID.String(),
		AuthorID:  n.AuthorID.String(),
		TastedAt:  n.TastedAt.Time,
		Rating:    int2Ptr(n.Rating),
		Aroma:     textOrEmpty(n.Aroma),
		Flavor:    textOrEmpty(n.Flavor),
		Mouthfeel: textOrEmpty(n.Mouthfeel),
		Finish:    textOrEmpty(n.Finish),
		Notes:     textOrEmpty(n.Notes),
	}
}

func int2Ptr(n pgtype.Int2) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int16)
	return &v
}

func (s *Server) handleCreateTastingNote(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	// Phase 1: only the batch owner can add tasting notes. Schema separates
	// author_id from brewer_id so non-owners can leave notes on public
	// batches in phase 2; for now, author_id always equals brewer_id.
	if !s.userOwnsBatch(r.Context(), user.ID, batchID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req createTastingNoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Rating != nil && (*req.Rating < 1 || *req.Rating > 5) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rating must be 1-5"})
		return
	}
	if req.Rating == nil && req.Aroma == "" && req.Flavor == "" &&
		req.Mouthfeel == "" && req.Finish == "" && req.Notes == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "tasting note must have at least one field set"})
		return
	}

	tasted := time.Now()
	if req.TastedAt != nil {
		tasted = *req.TastedAt
	}
	rating := pgtype.Int2{}
	if req.Rating != nil {
		rating = pgtype.Int2{Int16: int16(*req.Rating), Valid: true}
	}

	note, err := s.queries.CreateTastingNote(r.Context(), queries.CreateTastingNoteParams{
		BatchID:   batchID,
		AuthorID:  user.ID,
		TastedAt:  pgtype.Timestamptz{Time: tasted, Valid: true},
		Rating:    rating,
		Aroma:     optText(req.Aroma),
		Flavor:    optText(req.Flavor),
		Mouthfeel: optText(req.Mouthfeel),
		Finish:    optText(req.Finish),
		Notes:     optText(req.Notes),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, toTastingNoteView(note))
}

func (s *Server) handleListTastingNotes(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}
	if !s.userOwnsBatch(r.Context(), user.ID, batchID) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	rows, err := s.queries.ListTastingNotesForBatch(r.Context(), batchID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]tastingNoteView, 0, len(rows))
	for _, n := range rows {
		views = append(views, toTastingNoteView(n))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasting_notes": views})
}
