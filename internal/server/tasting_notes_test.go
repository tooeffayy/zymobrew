package server_test

import (
	"net/http"
	"testing"

	"zymobrew/internal/config"
)

func TestTastingNotes_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Spring Mead"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	// Add a tasting note with all fields
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/tasting-notes", map[string]any{
		"rating":    4,
		"aroma":     "honey, orange blossom",
		"flavor":    "balanced sweetness",
		"mouthfeel": "medium-bodied",
		"finish":    "clean",
		"notes":     "tasted at 6 months",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create note: got %d", resp.StatusCode)
	}
	created := decodeMap(t, resp)
	if r, _ := created["rating"].(float64); r != 4 {
		t.Fatalf("rating not preserved: %v", created["rating"])
	}
	if created["aroma"] != "honey, orange blossom" {
		t.Fatalf("aroma not preserved: %+v", created)
	}

	// Add a sparser one (just notes — no rating)
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/tasting-notes", map[string]any{
		"notes": "tasted at 1 year — improving",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create sparse note: got %d", resp.StatusCode)
	}

	// List — DESC by tasted_at, so most-recent first.
	resp = doJSON(t, srv, http.MethodGet, "/api/batches/"+id+"/tasting-notes", nil, cookies...)
	body := decodeMap(t, resp)
	notes, _ := body["tasting_notes"].([]any)
	if len(notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(notes))
	}
	first, _ := notes[0].(map[string]any)
	if first["notes"] != "tasted at 1 year — improving" {
		t.Fatalf("expected newest-first ordering, got %+v", first)
	}
}

func TestTastingNotes_RejectsEmpty(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "x"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/tasting-notes", map[string]any{}, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestTastingNotes_RejectsBadRating(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "x"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	for _, r := range []int{0, 6, -1, 100} {
		resp := doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/tasting-notes", map[string]any{
			"rating": r,
			"notes":  "x",
		}, cookies...)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("rating=%d: got %d, want 400", r, resp.StatusCode)
		}
	}
}

func TestTastingNotes_OwnershipIsolation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies := registerHelper(t, srv, "alice")
	bobCookies := registerHelper(t, srv, "bob")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Alice Mead"}, aliceCookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"create", http.MethodPost, "/api/batches/" + id + "/tasting-notes", map[string]any{"notes": "x"}},
		{"list", http.MethodGet, "/api/batches/" + id + "/tasting-notes", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, srv, tc.method, tc.path, tc.body, bobCookies...)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("got %d, want 404", resp.StatusCode)
			}
		})
	}
}
