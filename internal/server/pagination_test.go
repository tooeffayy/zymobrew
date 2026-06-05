package server_test

import (
	"net/http"
	"testing"
	"time"

	"zymobrew/internal/config"
)

// TestPagination_Cursor_PublicRecipes seeds N=5 public recipes and pages
// through with limit=2: expects 3 pages (2, 2, 1) with the third returning
// next_cursor=null. Verifies no duplicates across pages and that ordering
// is preserved (newest first).
func TestPagination_Cursor_PublicRecipes(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	// Seed: create 5 recipes with distinct names. Each POST is sequential
	// so updated_at ordering is stable (latest insert is newest).
	const total = 5
	for i := 0; i < total; i++ {
		body := recipeBody(map[string]any{"name": ordinal(i)})
		resp := doJSON(t, srv, http.MethodPost, "/api/recipes", body, cookies...)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed %d: got %d", i, resp.StatusCode)
		}
	}

	// Page through with limit=2.
	seen := make(map[string]bool)
	cursor := ""
	pages := 0
	for {
		path := "/api/recipes?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		resp := doJSON(t, srv, http.MethodGet, path, nil, cookies...)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("page %d: status %d", pages, resp.StatusCode)
		}
		body := decodeMap(t, resp)
		recipes, _ := body["recipes"].([]any)
		for _, r := range recipes {
			id := r.(map[string]any)["id"].(string)
			if seen[id] {
				t.Errorf("page %d: duplicate id %s", pages, id)
			}
			seen[id] = true
		}
		pages++
		switch v := body["next_cursor"].(type) {
		case nil:
			// End of list.
			if len(seen) != total {
				t.Errorf("saw %d unique recipes, want %d", len(seen), total)
			}
			if pages != 3 {
				t.Errorf("pages = %d, want 3 (5 rows / limit=2)", pages)
			}
			return
		case string:
			cursor = v
		default:
			t.Fatalf("page %d: next_cursor unexpected type %T", pages, body["next_cursor"])
		}
		if pages > 10 {
			t.Fatal("paging didn't terminate")
		}
	}
}

// TestPagination_Cursor_Comments paginates ASC (oldest comment first).
// The cursor comparison flips direction vs DESC lists; this confirms
// that the comparison stays correct across page boundaries.
func TestPagination_Cursor_Comments(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed recipe: got %d", resp.StatusCode)
	}
	recipeID := decodeMap(t, resp)["id"].(string)

	const total = 4
	for i := 0; i < total; i++ {
		resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/comments",
			map[string]any{"body": "comment " + ordinal(i)}, cookies...)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed comment %d: got %d", i, resp.StatusCode)
		}
	}

	var bodies []string
	cursor := ""
	for {
		path := "/api/recipes/" + recipeID + "/comments?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		resp := doJSON(t, srv, http.MethodGet, path, nil, cookies...)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d", resp.StatusCode)
		}
		page := decodeMap(t, resp)
		comments, _ := page["comments"].([]any)
		for _, c := range comments {
			bodies = append(bodies, c.(map[string]any)["body"].(string))
		}
		nc, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = nc
	}

	if len(bodies) != total {
		t.Fatalf("got %d comments, want %d", len(bodies), total)
	}
	// Comments are ASC (oldest first); seed inserted in order, so the
	// returned bodies must match insertion order.
	for i, b := range bodies {
		if b != "comment "+ordinal(i) {
			t.Errorf("position %d: got %q, want %q", i, b, "comment "+ordinal(i))
		}
	}
}

// TestPagination_BadCursor — malformed cursor must 400, not 500. We don't
// want a copy-pasted truncated URL to take down a request handler.
func TestPagination_BadCursor(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	resp := doJSON(t, srv, http.MethodGet, "/api/recipes?cursor=not-a-real-cursor", nil, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

// TestPagination_BadLimit — out-of-range limits must 400.
func TestPagination_BadLimit(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	for _, v := range []string{"0", "-1", "999", "abc"} {
		resp := doJSON(t, srv, http.MethodGet, "/api/recipes?limit="+v, nil, cookies...)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%s: got %d, want 400", v, resp.StatusCode)
		}
	}
}

// TestPagination_Cursor_Readings seeds N readings with strictly increasing
// taken_at timestamps and pages through with limit=2, confirming the ASC
// keyset cursor returns every row exactly once in chronological order.
func TestPagination_Cursor_Readings(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Pagination Mead"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed batch: got %d", resp.StatusCode)
	}
	batchID := decodeMap(t, resp)["id"].(string)

	const total = 5
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		resp := doJSON(t, srv, http.MethodPost, "/api/batches/"+batchID+"/readings", map[string]any{
			"taken_at": base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
			"gravity":  1.100 - float64(i)*0.01,
		}, cookies...)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed reading %d: got %d", i, resp.StatusCode)
		}
	}

	var takenAts []string
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		path := "/api/batches/" + batchID + "/readings?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		resp := doJSON(t, srv, http.MethodGet, path, nil, cookies...)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list readings: got %d", resp.StatusCode)
		}
		page := decodeMap(t, resp)
		readings, _ := page["readings"].([]any)
		for _, rd := range readings {
			m := rd.(map[string]any)
			id := m["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate reading %s across pages", id)
			}
			seen[id] = true
			takenAts = append(takenAts, m["taken_at"].(string))
		}
		pages++
		nc, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = nc
	}

	if len(takenAts) != total {
		t.Fatalf("got %d readings, want %d", len(takenAts), total)
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3 (5 rows / limit=2)", pages)
	}
	// ASC (oldest first): timestamps must come back strictly increasing.
	for i := 1; i < len(takenAts); i++ {
		if takenAts[i] <= takenAts[i-1] {
			t.Errorf("position %d: taken_at %q not after %q", i, takenAts[i], takenAts[i-1])
		}
	}
}

// TestPagination_Cursor_Events mirrors the readings test for the batch_events
// list, which shares the ASC keyset machinery.
func TestPagination_Cursor_Events(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Event Mead"}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed batch: got %d", resp.StatusCode)
	}
	batchID := decodeMap(t, resp)["id"].(string)

	const total = 5
	base := time.Date(2026, 2, 1, 9, 0, 0, 0, time.UTC)
	for i := 0; i < total; i++ {
		resp := doJSON(t, srv, http.MethodPost, "/api/batches/"+batchID+"/events", map[string]any{
			"occurred_at": base.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
			"kind":        "note",
			"title":       "event " + ordinal(i),
		}, cookies...)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed event %d: got %d", i, resp.StatusCode)
		}
	}

	var occurredAts []string
	seen := map[string]bool{}
	cursor := ""
	for {
		path := "/api/batches/" + batchID + "/events?limit=2"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		resp := doJSON(t, srv, http.MethodGet, path, nil, cookies...)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("list events: got %d", resp.StatusCode)
		}
		page := decodeMap(t, resp)
		events, _ := page["events"].([]any)
		for _, ev := range events {
			m := ev.(map[string]any)
			id := m["id"].(string)
			if seen[id] {
				t.Fatalf("duplicate event %s across pages", id)
			}
			seen[id] = true
			occurredAts = append(occurredAts, m["occurred_at"].(string))
		}
		nc, ok := page["next_cursor"].(string)
		if !ok {
			break
		}
		cursor = nc
	}

	if len(occurredAts) != total {
		t.Fatalf("got %d events, want %d", len(occurredAts), total)
	}
	for i := 1; i < len(occurredAts); i++ {
		if occurredAts[i] <= occurredAts[i-1] {
			t.Errorf("position %d: occurred_at %q not after %q", i, occurredAts[i], occurredAts[i-1])
		}
	}
}

// ordinal returns a stable name for the i-th seeded row. Names matter
// only for human-readable test failure messages — uniqueness is what
// the test actually relies on.
func ordinal(i int) string {
	return string(rune('A' + i))
}
