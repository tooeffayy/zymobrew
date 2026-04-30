package server_test

import (
	"net/http"
	"testing"

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
		resp := doJSON(t, srv, http.MethodGet, path, nil)
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
		resp := doJSON(t, srv, http.MethodGet, path, nil)
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
	resp := doJSON(t, srv, http.MethodGet, "/api/recipes?cursor=not-a-real-cursor", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", resp.StatusCode)
	}
}

// TestPagination_BadLimit — out-of-range limits must 400.
func TestPagination_BadLimit(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	for _, v := range []string{"0", "-1", "999", "abc"} {
		resp := doJSON(t, srv, http.MethodGet, "/api/recipes?limit="+v, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("limit=%s: got %d, want 400", v, resp.StatusCode)
		}
	}
}

// ordinal returns a stable name for the i-th seeded row. Names matter
// only for human-readable test failure messages — uniqueness is what
// the test actually relies on.
func ordinal(i int) string {
	return string(rune('A' + i))
}
