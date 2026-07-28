package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
)

// TestBatches_ReanchorOnStartedAtPatch covers the "re-materialization on
// re-anchor" gap: editing batch.started_at must shift fire_at on existing
// scheduled reminders, AND materialize any templates added since the
// initial materialization.
func TestBatches_ReanchorOnStartedAtPatch(t *testing.T) {
	srv, pool := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "rea_user1")

	recipeID := createRecipe(t, srv, cookies)
	tmpl1 := createTemplate(t, srv, cookies, recipeID, "Check OG", "batch_start", 60)

	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	batchID := createBatchWithRecipe(t, srv, cookies, recipeID, t1)

	// After initial create, exactly one reminder exists at T1+60m.
	got1 := readReminderFireAt(t, pool, batchID, tmpl1)
	want1 := t1.Add(60 * time.Minute)
	if !got1.Equal(want1) {
		t.Fatalf("initial fire_at: got %v, want %v", got1, want1)
	}

	// Add a SECOND template after the batch was already created. It hasn't
	// been materialized yet — we'll verify the re-anchor PATCH picks it up
	// alongside shifting the original.
	tmpl2 := createTemplate(t, srv, cookies, recipeID, "Racking nudge", "batch_start", 240)

	// PATCH started_at forward 48h.
	t2 := t1.Add(48 * time.Hour)
	resp := doJSON(t, srv, http.MethodPatch, "/api/batches/"+batchID, map[string]any{
		"started_at": t2.Format(time.RFC3339),
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}

	// Original reminder: re-anchored to T2+60m (UPDATE branch).
	got2 := readReminderFireAt(t, pool, batchID, tmpl1)
	want2 := t2.Add(60 * time.Minute)
	if !got2.Equal(want2) {
		t.Fatalf("after re-anchor (existing): got %v, want %v", got2, want2)
	}

	// New template: fresh row at T2+240m (INSERT branch).
	got3 := readReminderFireAt(t, pool, batchID, tmpl2)
	want3 := t2.Add(240 * time.Minute)
	if !got3.Equal(want3) {
		t.Fatalf("after re-anchor (new template): got %v, want %v", got3, want3)
	}

	// And only two reminders total — no duplicates.
	if n := countReminders(t, pool, batchID); n != 2 {
		t.Fatalf("expected 2 reminders, got %d", n)
	}
}

// TestBatches_ReanchorLeavesNonScheduledAlone verifies a cancelled reminder
// is not moved by a subsequent re-anchor — the status filter is the
// load-bearing safety here.
func TestBatches_ReanchorLeavesNonScheduledAlone(t *testing.T) {
	srv, pool := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "rea_user2")

	recipeID := createRecipe(t, srv, cookies)
	tmpl := createTemplate(t, srv, cookies, recipeID, "Original", "batch_start", 60)

	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	batchID := createBatchWithRecipe(t, srv, cookies, recipeID, t1)

	originalFireAt := readReminderFireAt(t, pool, batchID, tmpl)

	// Cancel the materialized reminder via direct SQL — testing through the
	// reminder API is out of scope for this test.
	if _, err := pool.Exec(context.Background(),
		`UPDATE reminders SET status = 'cancelled' WHERE batch_id = $1`, batchID); err != nil {
		t.Fatal(err)
	}

	// PATCH started_at to a new time.
	t2 := t1.Add(72 * time.Hour)
	resp := doJSON(t, srv, http.MethodPatch, "/api/batches/"+batchID, map[string]any{
		"started_at": t2.Format(time.RFC3339),
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}

	// The cancelled reminder's fire_at should be unchanged.
	gotAfter := readCancelledReminderFireAt(t, pool, batchID, tmpl)
	if !gotAfter.Equal(originalFireAt) {
		t.Fatalf("cancelled reminder was re-anchored: was %v, now %v", originalFireAt, gotAfter)
	}

	// MaterializeReminderTemplates' NOT EXISTS guard ignores cancelled rows,
	// so the patch should also have created a new scheduled row at T2+60m.
	// Total: 1 cancelled (at original time) + 1 scheduled (at T2+60m) = 2.
	if n := countTemplateReminders(t, pool, batchID, tmpl); n != 2 {
		t.Fatalf("expected 2 rows for template (1 cancelled + 1 new scheduled), got %d", n)
	}
}

// TestBatches_CustomEventAnchorMaterializes covers the custom_event anchor:
// a template targeting a batch-event kind materializes when an event of that
// kind is logged, re-anchors when the event's time is edited, and is not
// triggered by unrelated event kinds.
func TestBatches_CustomEventAnchorMaterializes(t *testing.T) {
	srv, pool := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "rea_custom1")

	recipeID := createRecipe(t, srv, cookies)
	// nutrient_addition has no dedicated anchor — custom_event is the only way
	// to hang a reminder off it. Fire 90m after the addition.
	tmpl := createCustomEventTemplate(t, srv, cookies, recipeID, "Next SNA", "nutrient_addition", 90)

	t1 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	batchID := createBatchWithRecipe(t, srv, cookies, recipeID, t1)

	// No matching event yet → nothing materialized.
	if n := countTemplateReminders(t, pool, batchID, tmpl); n != 0 {
		t.Fatalf("expected 0 reminders before any event, got %d", n)
	}

	// An unrelated event kind must not trigger the template.
	_ = createEvent(t, srv, cookies, batchID, "degas", t1.Add(6*time.Hour))
	if n := countTemplateReminders(t, pool, batchID, tmpl); n != 0 {
		t.Fatalf("unrelated event kind materialized a reminder: got %d", n)
	}

	// The matching event materializes one reminder at event_time + 90m.
	evTime := t1.Add(24 * time.Hour)
	eventID := createEvent(t, srv, cookies, batchID, "nutrient_addition", evTime)
	got := readReminderFireAt(t, pool, batchID, tmpl)
	want := evTime.Add(90 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("materialize fire_at: got %v, want %v", got, want)
	}

	// Editing the event's occurred_at re-anchors the scheduled reminder.
	evTime2 := evTime.Add(12 * time.Hour)
	resp := doJSON(t, srv, http.MethodPatch, "/api/batches/"+batchID+"/events/"+eventID, map[string]any{
		"occurred_at": evTime2.Format(time.RFC3339),
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch event: got %d", resp.StatusCode)
	}
	got2 := readReminderFireAt(t, pool, batchID, tmpl)
	want2 := evTime2.Add(90 * time.Minute)
	if !got2.Equal(want2) {
		t.Fatalf("re-anchor fire_at: got %v, want %v", got2, want2)
	}

	// Still exactly one reminder for the template — no duplicate on the edit.
	if n := countTemplateReminders(t, pool, batchID, tmpl); n != 1 {
		t.Fatalf("expected 1 reminder after re-anchor, got %d", n)
	}
}

// TestReminderTemplates_CustomEventRequiresKind verifies the API rejects a
// custom_event template with no kind to anchor to.
func TestReminderTemplates_CustomEventRequiresKind(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "rea_custom2")
	recipeID := createRecipe(t, srv, cookies)

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/reminder-templates", map[string]any{
		"title":          "Missing kind",
		"anchor":         "custom_event",
		"offset_minutes": 30,
	}, cookies...)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for custom_event without kind, got %d", resp.StatusCode)
	}
}

// --- test helpers --------------------------------------------------------

func createRecipe(t *testing.T, srv *server.Server, cookies []*http.Cookie) string {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/recipes", map[string]any{
		"name":       "Reanchor Test Mead",
		"brew_type":  "mead",
		"visibility": "public",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create recipe: got %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("recipe id missing: %+v", m)
	}
	return id
}

func createTemplate(t *testing.T, srv *server.Server, cookies []*http.Cookie, recipeID, title, anchor string, offsetMinutes int) uuid.UUID {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/reminder-templates", map[string]any{
		"title":          title,
		"anchor":         anchor,
		"offset_minutes": offsetMinutes,
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create template: got %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	idStr, _ := m["id"].(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("template id parse: %v (raw %q)", err, idStr)
	}
	return id
}

func createCustomEventTemplate(t *testing.T, srv *server.Server, cookies []*http.Cookie, recipeID, title, customEventKind string, offsetMinutes int) uuid.UUID {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/reminder-templates", map[string]any{
		"title":             title,
		"anchor":            "custom_event",
		"custom_event_kind": customEventKind,
		"offset_minutes":    offsetMinutes,
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create custom_event template: got %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	idStr, _ := m["id"].(string)
	id, err := uuid.Parse(idStr)
	if err != nil {
		t.Fatalf("template id parse: %v (raw %q)", err, idStr)
	}
	return id
}

func createEvent(t *testing.T, srv *server.Server, cookies []*http.Cookie, batchID, kind string, occurredAt time.Time) string {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/batches/"+batchID+"/events", map[string]any{
		"kind":        kind,
		"occurred_at": occurredAt.Format(time.RFC3339),
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create event (%s): got %d", kind, resp.StatusCode)
	}
	m := decodeMap(t, resp)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("event id missing: %+v", m)
	}
	return id
}

func createBatchWithRecipe(t *testing.T, srv *server.Server, cookies []*http.Cookie, recipeID string, startedAt time.Time) string {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{
		"name":       "Reanchor Batch",
		"recipe_id":  recipeID,
		"started_at": startedAt.Format(time.RFC3339),
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create batch: got %d", resp.StatusCode)
	}
	m := decodeMap(t, resp)
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("batch id missing: %+v", m)
	}
	return id
}

func readReminderFireAt(t *testing.T, pool *pgxpool.Pool, batchID string, templateID uuid.UUID) time.Time {
	t.Helper()
	var fireAt time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT fire_at FROM reminders WHERE batch_id = $1 AND template_id = $2 AND status = 'scheduled'`,
		batchID, templateID,
	).Scan(&fireAt)
	if err != nil {
		t.Fatalf("read scheduled fire_at: %v", err)
	}
	return fireAt.UTC()
}

func readCancelledReminderFireAt(t *testing.T, pool *pgxpool.Pool, batchID string, templateID uuid.UUID) time.Time {
	t.Helper()
	var fireAt time.Time
	err := pool.QueryRow(context.Background(),
		`SELECT fire_at FROM reminders WHERE batch_id = $1 AND template_id = $2 AND status = 'cancelled'`,
		batchID, templateID,
	).Scan(&fireAt)
	if err != nil {
		t.Fatalf("read cancelled fire_at: %v", err)
	}
	return fireAt.UTC()
}

func countReminders(t *testing.T, pool *pgxpool.Pool, batchID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reminders WHERE batch_id = $1`, batchID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func countTemplateReminders(t *testing.T, pool *pgxpool.Pool, batchID string, templateID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reminders WHERE batch_id = $1 AND template_id = $2`,
		batchID, templateID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
