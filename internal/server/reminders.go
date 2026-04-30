package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/queries"
)

const (
	maxReminderTitle = 200
	maxReminderDesc  = 2 * 1024
)

// --- view types -----------------------------------------------------------

type reminderView struct {
	ID                 string     `json:"id"`
	BatchID            *string    `json:"batch_id,omitempty"`
	Title              string     `json:"title"`
	Description        string     `json:"description,omitempty"`
	FireAt             time.Time  `json:"fire_at"`
	Status             string     `json:"status"`
	FiredAt            *time.Time `json:"fired_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	SuggestedEventKind *string    `json:"suggested_event_kind,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

func toReminderView(r queries.Reminder) reminderView {
	v := reminderView{
		ID:          r.ID.String(),
		Title:       r.Title,
		Description: textOrEmpty(r.Description),
		FireAt:      r.FireAt.Time,
		Status:      string(r.Status),
		CreatedAt:   r.CreatedAt.Time,
	}
	if r.BatchID.Valid {
		s := r.BatchID.UUID.String()
		v.BatchID = &s
	}
	if r.FiredAt.Valid {
		t := r.FiredAt.Time
		v.FiredAt = &t
	}
	if r.CompletedAt.Valid {
		t := r.CompletedAt.Time
		v.CompletedAt = &t
	}
	if r.SuggestedEventKind.Valid {
		s := string(r.SuggestedEventKind.EventKind)
		v.SuggestedEventKind = &s
	}
	return v
}

type notificationView struct {
	ID         string     `json:"id"`
	ReminderID *string    `json:"reminder_id,omitempty"`
	Kind       string     `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body,omitempty"`
	URLPath    string     `json:"url_path,omitempty"`
	ReadAt     *time.Time `json:"read_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toNotificationView(n queries.Notification) notificationView {
	v := notificationView{
		ID:        n.ID.String(),
		Kind:      n.Kind,
		Title:     n.Title,
		Body:      textOrEmpty(n.Body),
		URLPath:   textOrEmpty(n.UrlPath),
		CreatedAt: n.CreatedAt.Time,
	}
	if n.ReminderID.Valid {
		s := n.ReminderID.UUID.String()
		v.ReminderID = &s
	}
	if n.ReadAt.Valid {
		t := n.ReadAt.Time
		v.ReadAt = &t
	}
	return v
}

type notificationPrefsView struct {
	PushEnabled     bool    `json:"push_enabled"`
	EmailEnabled    bool    `json:"email_enabled"`
	QuietHoursStart *string `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd   *string `json:"quiet_hours_end,omitempty"`
	Timezone        string  `json:"timezone"`
}

func toPrefsView(p queries.NotificationPref) notificationPrefsView {
	v := notificationPrefsView{
		PushEnabled:  p.PushEnabled,
		EmailEnabled: p.EmailEnabled,
		Timezone:     p.Timezone,
	}
	if p.QuietHoursStart.Valid {
		h := p.QuietHoursStart.Microseconds / 3600000000
		m := (p.QuietHoursStart.Microseconds % 3600000000) / 60000000
		s := fmt.Sprintf("%02d:%02d", h, m)
		v.QuietHoursStart = &s
	}
	if p.QuietHoursEnd.Valid {
		h := p.QuietHoursEnd.Microseconds / 3600000000
		m := (p.QuietHoursEnd.Microseconds % 3600000000) / 60000000
		s := fmt.Sprintf("%02d:%02d", h, m)
		v.QuietHoursEnd = &s
	}
	return v
}

// --- batch reminder handlers ----------------------------------------------

func (s *Server) handleCreateReminder(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}

	var req struct {
		Title              string  `json:"title"`
		Description        string  `json:"description"`
		FireAt             string  `json:"fire_at"`
		SuggestedEventKind *string `json:"suggested_event_kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if req.Title == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "title is required"})
		return
	}
	if len(req.Title) > maxReminderTitle {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "title too long"})
		return
	}
	if len(req.Description) > maxReminderDesc {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "description too long"})
		return
	}
	if req.FireAt == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fire_at is required"})
		return
	}
	fireAt, err := time.Parse(time.RFC3339, req.FireAt)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fire_at must be RFC3339"})
		return
	}

	if _, err := s.queries.GetBatchForUser(r.Context(), queries.GetBatchForUserParams{
		ID: batchID, BrewerID: user.ID,
	}); errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	var sek queries.NullEventKind
	if req.SuggestedEventKind != nil {
		sek = queries.NullEventKind{EventKind: queries.EventKind(*req.SuggestedEventKind), Valid: true}
	}

	rem, err := s.queries.CreateReminder(r.Context(), queries.CreateReminderParams{
		BatchID:            uuid.NullUUID{UUID: batchID, Valid: true},
		UserID:             user.ID,
		Title:              req.Title,
		Description:        pgtype.Text{String: req.Description, Valid: req.Description != ""},
		FireAt:             pgtype.Timestamptz{Time: fireAt, Valid: true},
		SuggestedEventKind: sek,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusCreated, toReminderView(rem))
}

func (s *Server) handleListReminders(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	batchID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid batch id"})
		return
	}

	if _, err := s.queries.GetBatchForUser(r.Context(), queries.GetBatchForUserParams{
		ID: batchID, BrewerID: user.ID,
	}); errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	} else if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	rems, err := s.queries.ListBatchReminders(r.Context(), queries.ListBatchRemindersParams{
		BatchID: uuid.NullUUID{UUID: batchID, Valid: true},
		UserID:  user.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	views := make([]reminderView, 0, len(rems))
	for _, rem := range rems {
		views = append(views, toReminderView(rem))
	}
	writeJSON(w, http.StatusOK, views)
}

func (s *Server) handleUpdateReminder(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	reminderID, err := uuid.Parse(chi.URLParam(r, "reminderId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reminder id"})
		return
	}

	var req struct {
		Title              *string `json:"title"`
		Description        *string `json:"description"`
		FireAt             *string `json:"fire_at"`
		Status             *string `json:"status"`
		SuggestedEventKind *string `json:"suggested_event_kind"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	if req.Title != nil && len(*req.Title) > maxReminderTitle {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "title too long"})
		return
	}
	if req.Description != nil && len(*req.Description) > maxReminderDesc {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "description too long"})
		return
	}

	params := queries.UpdateReminderParams{
		ID:     reminderID,
		UserID: user.ID,
	}
	if req.Title != nil {
		params.Title = pgtype.Text{String: *req.Title, Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	if req.FireAt != nil {
		t, err := time.Parse(time.RFC3339, *req.FireAt)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "fire_at must be RFC3339"})
			return
		}
		params.FireAt = pgtype.Timestamptz{Time: t, Valid: true}
	}
	if req.Status != nil {
		allowed := map[string]bool{
			"scheduled": true,
			"snoozed":   true,
			"completed": true,
			"dismissed": true,
		}
		if !allowed[*req.Status] {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid status"})
			return
		}
		params.Status = queries.NullReminderStatus{ReminderStatus: queries.ReminderStatus(*req.Status), Valid: true}
	}
	if req.SuggestedEventKind != nil {
		params.SuggestedEventKind = queries.NullEventKind{EventKind: queries.EventKind(*req.SuggestedEventKind), Valid: true}
	}

	rem, err := s.queries.UpdateReminder(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	writeJSON(w, http.StatusOK, toReminderView(rem))
}

func (s *Server) handleDeleteReminder(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	reminderID, err := uuid.Parse(chi.URLParam(r, "reminderId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reminder id"})
		return
	}

	n, err := s.queries.CancelReminder(r.Context(), queries.CancelReminderParams{
		ID:     reminderID,
		UserID: user.ID,
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

// --- notification handlers ------------------------------------------------

func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	p, ok := parseListPagination(w, r)
	if !ok {
		return
	}

	notifs, err := s.queries.ListNotifications(r.Context(), queries.ListNotificationsParams{
		UserID:   user.ID,
		CursorTs: p.CursorTs,
		CursorID: p.CursorID,
		LimitN:   p.Limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	views := make([]notificationView, 0, len(notifs))
	for _, n := range notifs {
		views = append(views, toNotificationView(n))
	}
	next := nextCursor(len(notifs), p.Limit, func() (time.Time, uuid.UUID) {
		last := notifs[len(notifs)-1]
		return last.CreatedAt.Time, last.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"notifications": views, "next_cursor": nullableCursor(next)})
}

func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	notifID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	n, err := s.queries.MarkNotificationRead(r.Context(), queries.MarkNotificationReadParams{
		ID:     notifID,
		UserID: user.ID,
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

func (s *Server) handleMarkAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	if err := s.queries.MarkAllNotificationsRead(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- notification prefs handlers -----------------------------------------

func (s *Server) handleGetNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	prefs, err := s.queries.GetNotificationPrefs(r.Context(), user.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, notificationPrefsView{
			PushEnabled:  true,
			EmailEnabled: false,
			Timezone:     "UTC",
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, toPrefsView(prefs))
}

func (s *Server) handleUpdateNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	existing, err := s.queries.GetNotificationPrefs(r.Context(), user.ID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		existing = queries.NotificationPref{
			UserID:       user.ID,
			PushEnabled:  true,
			EmailEnabled: false,
			Timezone:     "UTC",
		}
	}

	var req struct {
		PushEnabled     *bool   `json:"push_enabled"`
		EmailEnabled    *bool   `json:"email_enabled"`
		QuietHoursStart *string `json:"quiet_hours_start"`
		QuietHoursEnd   *string `json:"quiet_hours_end"`
		Timezone        *string `json:"timezone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	params := queries.UpsertNotificationPrefsParams{
		UserID:          user.ID,
		PushEnabled:     existing.PushEnabled,
		EmailEnabled:    existing.EmailEnabled,
		QuietHoursStart: existing.QuietHoursStart,
		QuietHoursEnd:   existing.QuietHoursEnd,
		Timezone:        existing.Timezone,
	}
	if req.PushEnabled != nil {
		params.PushEnabled = *req.PushEnabled
	}
	if req.EmailEnabled != nil {
		params.EmailEnabled = *req.EmailEnabled
	}
	if req.Timezone != nil {
		if _, err := time.LoadLocation(*req.Timezone); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid timezone"})
			return
		}
		params.Timezone = *req.Timezone
	}
	if req.QuietHoursStart != nil {
		t, err := parseHHMM(*req.QuietHoursStart)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "quiet_hours_start must be HH:MM"})
			return
		}
		params.QuietHoursStart = t
	}
	if req.QuietHoursEnd != nil {
		t, err := parseHHMM(*req.QuietHoursEnd)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "quiet_hours_end must be HH:MM"})
			return
		}
		params.QuietHoursEnd = t
	}

	prefs, err := s.queries.UpsertNotificationPrefs(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, toPrefsView(prefs))
}

// parseHHMM parses "HH:MM" into a pgtype.Time (microseconds since midnight).
func parseHHMM(s string) (pgtype.Time, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return pgtype.Time{}, err
	}
	micros := int64(t.Hour())*3600000000 + int64(t.Minute())*60000000
	return pgtype.Time{Microseconds: micros, Valid: true}, nil
}
