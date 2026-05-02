package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"zymobrew/internal/config"
)

func TestEvents_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Spring Mead"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	// Create event with structured details (Fermaid-O nutrient addition)
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{
		"kind":  "nutrient_addition",
		"title": "Day 2 nutrients",
		"details": map[string]any{
			"product":  "Fermaid-O",
			"amount_g": 1.5,
		},
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create event: got %d", resp.StatusCode)
	}
	created := decodeMap(t, resp)
	if created["kind"] != "nutrient_addition" {
		t.Fatalf("kind not preserved: %+v", created)
	}
	details, _ := created["details"].(map[string]any)
	if details["product"] != "Fermaid-O" {
		t.Fatalf("details JSONB round-trip failed: %+v", details)
	}
	if g, _ := details["amount_g"].(float64); g != 1.5 {
		t.Fatalf("amount_g round-trip lost value: %v", details["amount_g"])
	}

	// Add a second event
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{
		"kind":        "rack",
		"description": "to secondary",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create rack event: got %d", resp.StatusCode)
	}

	// List
	resp = doJSON(t, srv, http.MethodGet, "/api/batches/"+id+"/events", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list events: got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	events, _ := body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestEvents_RejectsBadKind(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "x"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty-kind", map[string]any{"kind": ""}},
		{"bogus-kind", map[string]any{"kind": "fluffify"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", c.body, cookies...)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestEvents_RejectsNonObjectDetails(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "x"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	// details=[1,2,3] is valid JSON but not the object shape we expect.
	// httptest path: doJSON re-marshals through map[string]any, but we need
	// raw bytes to send a non-object — use json.RawMessage shenanigans here.
	body := map[string]any{
		"kind":    "note",
		"details": json.RawMessage(`[1,2,3]`),
	}
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", body, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestEvents_OwnershipIsolation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies := registerHelper(t, srv, "alice")
	bobCookies := registerHelper(t, srv, "bob")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Alice Mead"}, aliceCookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	// Seed an event Bob will try to touch.
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "note"}, aliceCookies...)
	eventID, _ := decodeMap(t, resp)["id"].(string)

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"create", http.MethodPost, "/api/batches/" + id + "/events", map[string]any{"kind": "note"}},
		{"list", http.MethodGet, "/api/batches/" + id + "/events", nil},
		{"update", http.MethodPatch, "/api/batches/" + id + "/events/" + eventID, map[string]any{"description": "hi"}},
		{"delete", http.MethodDelete, "/api/batches/" + id + "/events/" + eventID, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, srv, tc.method, tc.path, tc.body, bobCookies...)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("got %d, want 404", resp.StatusCode)
			}
		})
	}
}

func TestEvents_UpdateAndDelete(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Spring Mead"}, cookies...)
	batchID, _ := decodeMap(t, resp)["id"].(string)

	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+batchID+"/events", map[string]any{
		"kind":        "note",
		"description": "first draft",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create event: got %d", resp.StatusCode)
	}
	eventID, _ := decodeMap(t, resp)["id"].(string)

	// PATCH the description and switch the kind. Server should accept both.
	resp = doJSON(t, srv, http.MethodPatch, "/api/batches/"+batchID+"/events/"+eventID, map[string]any{
		"kind":        "rack",
		"description": "racked to secondary",
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch event: got %d", resp.StatusCode)
	}
	updated := decodeMap(t, resp)
	if updated["kind"] != "rack" {
		t.Fatalf("kind not updated: %+v", updated)
	}
	if updated["description"] != "racked to secondary" {
		t.Fatalf("description not updated: %+v", updated)
	}

	// PATCH with bogus kind → 400.
	resp = doJSON(t, srv, http.MethodPatch, "/api/batches/"+batchID+"/events/"+eventID, map[string]any{
		"kind": "fluffify",
	}, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad kind on update: got %d, want 400", resp.StatusCode)
	}

	// DELETE → 204, then GET list shows zero events.
	resp = doJSON(t, srv, http.MethodDelete, "/api/batches/"+batchID+"/events/"+eventID, nil, cookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete event: got %d", resp.StatusCode)
	}
	resp = doJSON(t, srv, http.MethodGet, "/api/batches/"+batchID+"/events", nil, cookies...)
	body := decodeMap(t, resp)
	events, _ := body["events"].([]any)
	if len(events) != 0 {
		t.Fatalf("expected 0 events after delete, got %d", len(events))
	}

	// Second DELETE on the same id → 404.
	resp = doJSON(t, srv, http.MethodDelete, "/api/batches/"+batchID+"/events/"+eventID, nil, cookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete after delete: got %d, want 404", resp.StatusCode)
	}
}

// TestEvents_AutoAdvanceStage covers the implicit stage transitions wired
// into handleCreateBatchEvent + handleUpdateBatchEvent: pitch → primary,
// rack → secondary, bottle → bottled. The transition is forward-only —
// a stale "pitch" logged after racking should not roll the batch back.
func TestEvents_AutoAdvanceStage(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	getStage := func(t *testing.T, id string) string {
		t.Helper()
		r := doJSON(t, srv, http.MethodGet, "/api/batches/"+id, nil, cookies...)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("get batch: got %d", r.StatusCode)
		}
		stage, _ := decodeMap(t, r)["stage"].(string)
		return stage
	}

	// Create a fresh batch — defaults to "planning".
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Spring Mead"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)
	if got := getStage(t, id); got != "planning" {
		t.Fatalf("initial stage: want planning, got %q", got)
	}

	// pitch → primary
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "pitch"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("pitch event: got %d", resp.StatusCode)
	}
	if got := getStage(t, id); got != "primary" {
		t.Fatalf("after pitch: want primary, got %q", got)
	}

	// rack → secondary
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "rack"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("rack event: got %d", resp.StatusCode)
	}
	if got := getStage(t, id); got != "secondary" {
		t.Fatalf("after rack: want secondary, got %q", got)
	}

	// A second (stale) pitch event must not roll the batch backwards.
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "pitch"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("late pitch: got %d", resp.StatusCode)
	}
	if got := getStage(t, id); got != "secondary" {
		t.Fatalf("after late pitch: want secondary (no rollback), got %q", got)
	}

	// bottle → bottled, and bottled_at is stamped.
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "bottle"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bottle event: got %d", resp.StatusCode)
	}
	r := doJSON(t, srv, http.MethodGet, "/api/batches/"+id, nil, cookies...)
	bottled := decodeMap(t, r)
	if bottled["stage"] != "bottled" {
		t.Fatalf("after bottle: want bottled, got %q", bottled["stage"])
	}
	if _, ok := bottled["bottled_at"].(string); !ok {
		t.Fatalf("bottled_at not stamped: %+v", bottled)
	}

	// Non-stage-advancing events leave the stage alone.
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "note", "description": "tastes great"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("note event: got %d", resp.StatusCode)
	}
	if got := getStage(t, id); got != "bottled" {
		t.Fatalf("after note: want bottled, got %q", got)
	}

	// Editing a "note" into a "rack" on a bottled batch must also not roll back.
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/events", map[string]any{"kind": "note"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("note for edit: got %d", resp.StatusCode)
	}
	noteID, _ := decodeMap(t, resp)["id"].(string)
	resp = doJSON(t, srv, http.MethodPatch, "/api/batches/"+id+"/events/"+noteID, map[string]any{"kind": "rack"}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch to rack: got %d", resp.StatusCode)
	}
	if got := getStage(t, id); got != "bottled" {
		t.Fatalf("after edit-to-rack: want bottled (no rollback), got %q", got)
	}
}
