package server_test

import (
	"net/http"
	"strings"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
)

// recipeBody is a convenience helper for recipe POST/PATCH bodies.
func recipeBody(overrides map[string]any) map[string]any {
	base := map[string]any{
		"name":       "Orange Blossom Mead",
		"brew_type":  "mead",
		"visibility": "public",
		"ingredients": []map[string]any{
			{"kind": "honey", "name": "Orange Blossom", "amount": 3.5, "unit": "kg", "sort_order": 0},
			{"kind": "water", "name": "Filtered Water", "amount": 15.0, "unit": "L", "sort_order": 1},
		},
	}
	for k, v := range overrides {
		base[k] = v
	}
	return base
}

func TestRecipe_Create_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(map[string]any{
		"style":       "Traditional",
		"description": "A classic",
		"target_og":   1.110,
		"message":     "Initial recipe",
	}), cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)

	if body["name"] != "Orange Blossom Mead" {
		t.Errorf("name: got %v", body["name"])
	}
	if body["brew_type"] != "mead" {
		t.Errorf("brew_type: got %v", body["brew_type"])
	}
	if body["visibility"] != "public" {
		t.Errorf("visibility: got %v", body["visibility"])
	}
	if body["revision_number"] != float64(1) {
		t.Errorf("revision_number: got %v", body["revision_number"])
	}
	if body["revision_count"] != float64(1) {
		t.Errorf("revision_count: got %v", body["revision_count"])
	}
	if body["message"] != "Initial recipe" {
		t.Errorf("message: got %v", body["message"])
	}
	ings, ok := body["ingredients"].([]any)
	if !ok || len(ings) != 2 {
		t.Errorf("ingredients: got %v", body["ingredients"])
	}
	if body["id"] == nil || body["created_at"] == nil {
		t.Errorf("missing id or created_at: %v", body)
	}
}

func TestRecipe_Create_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestRecipe_Create_Validation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty_name", recipeBody(map[string]any{"name": ""})},
		{"name_too_long", recipeBody(map[string]any{"name": strings.Repeat("a", 201)})},
		{"bad_brew_type", recipeBody(map[string]any{"brew_type": "beer"})},
		{"bad_visibility", recipeBody(map[string]any{"visibility": "invalid"})},
		{"ingredient_no_kind", recipeBody(map[string]any{
			"ingredients": []map[string]any{{"kind": "", "name": "Honey"}},
		})},
		{"ingredient_no_name", recipeBody(map[string]any{
			"ingredients": []map[string]any{{"kind": "honey", "name": ""}},
		})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/recipes", c.body, cookies...)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("%s: got %d, want 400", c.name, resp.StatusCode)
			}
		})
	}
}

func TestRecipe_Get_NotFound(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodGet, "/api/recipes/00000000-0000-0000-0000-000000000000", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.StatusCode)
	}
}

func TestRecipe_Get_Public_NoAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	// Public recipe accessible without auth.
	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	if body["name"] != "Orange Blossom Mead" {
		t.Errorf("name: got %v", body["name"])
	}
}

func TestRecipe_Get_Private_NotOwner(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	aliceCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes",
		recipeBody(map[string]any{"visibility": "private"}), aliceCookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	// Bob cannot see Alice's private recipe.
	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID, nil, bobCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob: got %d, want 404", resp.StatusCode)
	}

	// Anonymous cannot see it either.
	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("anon: got %d, want 404", resp.StatusCode)
	}

	// Alice herself can see it.
	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID, nil, aliceCookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alice: got %d, want 200", resp.StatusCode)
	}
}

func TestRecipe_List_Public(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	doJSON(t, srv, http.MethodPost, "/api/recipes",
		recipeBody(map[string]any{"name": "Private Mead", "visibility": "private"}), cookies...)

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	recipes, ok := body["recipes"].([]any)
	if !ok {
		t.Fatal("missing recipes array")
	}
	// Private recipe must not appear in the public feed.
	if len(recipes) != 1 {
		t.Errorf("public feed: got %d recipes, want 1", len(recipes))
	}
}

func TestRecipe_ListMine_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodGet, "/api/recipes/mine", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestRecipe_ListMine_IncludesPrivate(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	doJSON(t, srv, http.MethodPost, "/api/recipes",
		recipeBody(map[string]any{"name": "Secret Mead", "visibility": "private"}), cookies...)

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/mine", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	recipes, _ := body["recipes"].([]any)
	if len(recipes) != 2 {
		t.Errorf("mine: got %d recipes, want 2", len(recipes))
	}
}

func TestRecipe_Update_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	// PATCH the name and message; ingredients unchanged.
	resp = doJSON(t, srv, http.MethodPatch, "/api/recipes/"+recipeID, map[string]any{
		"name":    "Wildflower Mead",
		"message": "renamed",
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}
	var patched map[string]any
	decode(t, resp, &patched)

	if patched["name"] != "Wildflower Mead" {
		t.Errorf("name: got %v", patched["name"])
	}
	if patched["revision_number"] != float64(2) {
		t.Errorf("revision_number: got %v", patched["revision_number"])
	}
	if patched["revision_count"] != float64(2) {
		t.Errorf("revision_count: got %v", patched["revision_count"])
	}
	if patched["message"] != "renamed" {
		t.Errorf("message: got %v", patched["message"])
	}
	// Ingredients unchanged.
	ings, _ := patched["ingredients"].([]any)
	if len(ings) != 2 {
		t.Errorf("ingredients should be unchanged, got %d", len(ings))
	}
}

func TestRecipe_Update_IngredientsReplace(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	// Replace ingredients with a single item.
	resp = doJSON(t, srv, http.MethodPatch, "/api/recipes/"+recipeID, map[string]any{
		"ingredients": []map[string]any{
			{"kind": "honey", "name": "Clover Honey", "amount": 4.0, "unit": "kg", "sort_order": 0},
		},
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	ings, _ := body["ingredients"].([]any)
	if len(ings) != 1 {
		t.Errorf("ingredients: got %d, want 1", len(ings))
	}
	ing0, _ := ings[0].(map[string]any)
	if ing0["name"] != "Clover Honey" {
		t.Errorf("ingredient name: got %v", ing0["name"])
	}
}

func TestRecipe_Update_Forbidden(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	aliceCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), aliceCookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	resp = doJSON(t, srv, http.MethodPatch, "/api/recipes/"+recipeID, map[string]any{
		"name": "Stolen Mead",
	}, bobCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob patch: got %d, want 404", resp.StatusCode)
	}
}

func TestRecipe_Delete_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	resp = doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID, nil, cookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after delete: got %d, want 404", resp.StatusCode)
	}
}

func TestRecipe_Delete_Forbidden(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	aliceCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), aliceCookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	resp = doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID, nil, bobCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob delete: got %d, want 404", resp.StatusCode)
	}
}

func TestRecipe_Revisions_List(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(map[string]any{"message": "v1"}), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	doJSON(t, srv, http.MethodPatch, "/api/recipes/"+recipeID, map[string]any{"message": "v2"}, cookies...)
	doJSON(t, srv, http.MethodPatch, "/api/recipes/"+recipeID, map[string]any{"message": "v3"}, cookies...)

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/revisions", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	revisions, _ := body["revisions"].([]any)
	if len(revisions) != 3 {
		t.Fatalf("revisions: got %d, want 3", len(revisions))
	}
	// Newest first.
	rev0, _ := revisions[0].(map[string]any)
	if rev0["revision_number"] != float64(3) {
		t.Errorf("first revision should be 3, got %v", rev0["revision_number"])
	}
	if rev0["message"] != "v3" {
		t.Errorf("message: got %v", rev0["message"])
	}
}

func TestRecipe_Revision_GetByNumber(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(map[string]any{
		"message":     "initial",
		"description": "v1 desc",
	}), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	doJSON(t, srv, http.MethodPatch, "/api/recipes/"+recipeID, map[string]any{
		"description": "v2 desc",
		"message":     "second revision",
	}, cookies...)

	// Fetch revision 1 — should have original description and ingredients snapshot.
	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/revisions/1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	if body["revision_number"] != float64(1) {
		t.Errorf("revision_number: got %v", body["revision_number"])
	}
	if body["message"] != "initial" {
		t.Errorf("message: got %v", body["message"])
	}
	// Ingredients are a JSONB snapshot.
	if body["ingredients"] == nil {
		t.Error("missing ingredients in revision")
	}
}

func TestRecipe_Revision_NotFound(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(nil), cookies...)
	var created map[string]any
	decode(t, resp, &created)
	recipeID := created["id"].(string)

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/revisions/99", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.StatusCode)
	}
}

// --- fork tests ---

func registerAndCreateRecipe(t *testing.T, srv *server.Server, username, visibility string) ([]*http.Cookie, string) {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    username + "@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()
	resp = doJSON(t, srv, http.MethodPost, "/api/recipes", recipeBody(map[string]any{
		"visibility": visibility,
	}), cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create recipe: got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	return cookies, body["id"].(string)
}

func TestRecipe_Fork_Public(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, srcID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", map[string]any{
		"name":    "Bob's Variant",
		"message": "customised for winter",
	}, bobCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fork: got %d", resp.StatusCode)
	}
	var fork map[string]any
	decode(t, resp, &fork)

	if fork["parent_id"] != srcID {
		t.Errorf("parent_id: got %v, want %v", fork["parent_id"], srcID)
	}
	if fork["name"] != "Bob's Variant" {
		t.Errorf("name: got %v", fork["name"])
	}
	if fork["visibility"] != "private" {
		t.Errorf("fork should default to private, got %v", fork["visibility"])
	}
	if fork["revision_number"] != float64(1) {
		t.Errorf("revision_number: got %v", fork["revision_number"])
	}
	if fork["message"] != "customised for winter" {
		t.Errorf("message: got %v", fork["message"])
	}
	ings, _ := fork["ingredients"].([]any)
	if len(ings) != 2 {
		t.Errorf("ingredients: got %d, want 2", len(ings))
	}
}

func TestRecipe_Fork_Unlisted(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, srcID := registerAndCreateRecipe(t, srv, "alice", "unlisted")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", nil, bobCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fork unlisted: got %d", resp.StatusCode)
	}
	var fork map[string]any
	decode(t, resp, &fork)
	if fork["parent_id"] != srcID {
		t.Errorf("parent_id: got %v", fork["parent_id"])
	}
}

func TestRecipe_Fork_Private_Returns404(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, srcID := registerAndCreateRecipe(t, srv, "alice", "private")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", nil, bobCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("fork private: got %d, want 404", resp.StatusCode)
	}
}

func TestRecipe_Fork_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, srcID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestRecipe_Fork_IncrementsForkCount(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, srcID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()

	doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", nil, bobCookies...)

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+srcID, nil)
	var src map[string]any
	decode(t, resp, &src)
	if src["fork_count"] != float64(1) {
		t.Errorf("fork_count: got %v, want 1", src["fork_count"])
	}
}

func TestRecipe_Fork_SelfFork(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies, srcID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", nil, aliceCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("self-fork: got %d", resp.StatusCode)
	}
	var fork map[string]any
	decode(t, resp, &fork)
	if fork["parent_id"] != srcID {
		t.Errorf("parent_id: got %v", fork["parent_id"])
	}
}

func TestRecipe_Fork_ForkOfFork(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, srcID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "supersecret",
	})
	bobCookies := resp.Cookies()
	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+srcID+"/fork", nil, bobCookies...)
	var bobFork map[string]any
	decode(t, resp, &bobFork)

	// Bob's fork is private; patch it to public so Carol can fork it.
	forkID := bobFork["id"].(string)
	doJSON(t, srv, http.MethodPatch, "/api/recipes/"+forkID, map[string]any{"visibility": "public"}, bobCookies...)

	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "carol",
		"email":    "carol@example.com",
		"password": "supersecret",
	})
	carolCookies := resp.Cookies()
	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+forkID+"/fork", nil, carolCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fork-of-fork: got %d", resp.StatusCode)
	}
	var carolFork map[string]any
	decode(t, resp, &carolFork)
	if carolFork["parent_id"] != forkID {
		t.Errorf("parent_id: got %v, want %v", carolFork["parent_id"], forkID)
	}
}

// --- Comments ---

func TestRecipe_Comments_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/comments",
		map[string]string{"body": "Great recipe!"}, aliceCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create comment: got %d", resp.StatusCode)
	}
	var comment map[string]any
	decode(t, resp, &comment)
	if comment["body"] != "Great recipe!" {
		t.Errorf("body: got %v", comment["body"])
	}
	if comment["author_username"] != "alice" {
		t.Errorf("author_username: got %v", comment["author_username"])
	}
	if comment["id"] == nil {
		t.Error("missing id")
	}

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/comments", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list comments: got %d", resp.StatusCode)
	}
	var list map[string]any
	decode(t, resp, &list)
	comments, ok := list["comments"].([]any)
	if !ok || len(comments) != 1 {
		t.Errorf("comments: got %v", list["comments"])
	}
}

func TestRecipe_Comments_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/comments",
		map[string]string{"body": "no auth"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestRecipe_Comments_EmptyBody(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/comments",
		map[string]string{"body": ""}, aliceCookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestRecipe_Comments_PrivateRecipe(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, recipeID := registerAndCreateRecipe(t, srv, "alice", "private")

	// Register bob and try to comment/list on alice's private recipe.
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob", "email": "bob@example.com", "password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/comments", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("list unauthenticated: want 404, got %d", resp.StatusCode)
	}
	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/comments",
		map[string]string{"body": "sneaky"}, bobCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("create as bob: want 404, got %d", resp.StatusCode)
	}
}

func TestRecipe_Comments_Delete(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob", "email": "bob@example.com", "password": "supersecret",
	})
	bobCookies := resp.Cookies()

	// Bob posts a comment.
	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/comments",
		map[string]string{"body": "bob's comment"}, bobCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bob create comment: got %d", resp.StatusCode)
	}
	var comment map[string]any
	decode(t, resp, &comment)
	commentID := comment["id"].(string)

	// Alice cannot delete bob's comment.
	resp = doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID+"/comments/"+commentID, nil, aliceCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("alice delete bob's comment: want 404, got %d", resp.StatusCode)
	}

	// Bob can delete his own comment.
	resp = doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID+"/comments/"+commentID, nil, bobCookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("bob delete own comment: want 204, got %d", resp.StatusCode)
	}

	// Verify the comment is gone.
	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/comments", nil)
	var list map[string]any
	decode(t, resp, &list)
	comments := list["comments"].([]any)
	if len(comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(comments))
	}
}

// --- Likes ---

func TestRecipe_Like_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob", "email": "bob@example.com", "password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("like: want 204, got %d", resp.StatusCode)
	}

	resp = doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("unlike: want 204, got %d", resp.StatusCode)
	}
}

func TestRecipe_Like_Idempotent(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob", "email": "bob@example.com", "password": "supersecret",
	})
	bobCookies := resp.Cookies()

	// Like twice — both should succeed.
	doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("like idempotent: want 204, got %d", resp.StatusCode)
	}

	// Unlike twice — both should succeed.
	doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	resp = doJSON(t, srv, http.MethodDelete, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("unlike idempotent: want 204, got %d", resp.StatusCode)
	}
}

func TestRecipe_Like_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, recipeID := registerAndCreateRecipe(t, srv, "alice", "public")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/like", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("like unauthenticated: want 401, got %d", resp.StatusCode)
	}
}

func TestRecipe_Like_PrivateRecipe(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	_, recipeID := registerAndCreateRecipe(t, srv, "alice", "private")

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob", "email": "bob@example.com", "password": "supersecret",
	})
	bobCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/recipes/"+recipeID+"/like", nil, bobCookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("like private recipe: want 404, got %d", resp.StatusCode)
	}
}
