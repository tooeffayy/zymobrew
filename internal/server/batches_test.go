package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
)

// registerHelper creates a user and returns their session cookie. Used to
// log a user in for the batch test cases.
func registerHelper(t *testing.T, srv *server.Server, username string) []*http.Cookie {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username,
		"email":    username + "@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %q: got %d", username, resp.StatusCode)
	}
	return resp.Cookies()
}

func decodeMap(t *testing.T, r *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestBatches_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	// Create
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{
		"name": "Spring Mead",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d", resp.StatusCode)
	}
	created := decodeMap(t, resp)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %+v", created)
	}
	if created["brew_type"] != "mead" || created["stage"] != "planning" {
		t.Fatalf("defaults wrong: %+v", created)
	}

	// List
	resp = doJSON(t, srv, http.MethodGet, "/api/batches", nil, cookies...)
	listed := decodeMap(t, resp)
	if arr, _ := listed["batches"].([]any); len(arr) != 1 {
		t.Fatalf("expected 1 batch, got %d (%+v)", len(arr), listed)
	}

	// Get
	resp = doJSON(t, srv, http.MethodGet, "/api/batches/"+id, nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: got %d", resp.StatusCode)
	}

	// Patch
	resp = doJSON(t, srv, http.MethodPatch, "/api/batches/"+id, map[string]any{
		"stage": "primary",
		"notes": "pitched K1-V1116",
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}
	updated := decodeMap(t, resp)
	if updated["stage"] != "primary" || updated["notes"] != "pitched K1-V1116" {
		t.Fatalf("patch did not apply: %+v", updated)
	}

	// Add a reading
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/readings", map[string]any{
		"gravity":       1.105,
		"temperature_c": 21.5,
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create reading: got %d", resp.StatusCode)
	}

	// List readings
	resp = doJSON(t, srv, http.MethodGet, "/api/batches/"+id+"/readings", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list readings: got %d", resp.StatusCode)
	}
	readingsBody := decodeMap(t, resp)
	rs, _ := readingsBody["readings"].([]any)
	if len(rs) != 1 {
		t.Fatalf("expected 1 reading, got %d", len(rs))
	}
	first, _ := rs[0].(map[string]any)
	if g, _ := first["gravity"].(float64); g != 1.105 {
		t.Fatalf("gravity round-trip lost precision: %v", first["gravity"])
	}

	// Delete
	resp = doJSON(t, srv, http.MethodDelete, "/api/batches/"+id, nil, cookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}

	// Get after delete
	resp = doJSON(t, srv, http.MethodGet, "/api/batches/"+id, nil, cookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: got %d", resp.StatusCode)
	}
}

func TestBatches_RequireAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodGet, "/api/batches", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestBatches_OwnershipIsolation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	aliceCookies := registerHelper(t, srv, "alice")
	bobCookies := registerHelper(t, srv, "bob")

	// Alice creates a batch.
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "Alice Mead"}, aliceCookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("alice create: got %d", resp.StatusCode)
	}
	id, _ := decodeMap(t, resp)["id"].(string)

	// Bob can't see, edit, delete, or add readings to Alice's batch.
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"get", http.MethodGet, "/api/batches/" + id, nil},
		{"patch", http.MethodPatch, "/api/batches/" + id, map[string]any{"name": "stolen"}},
		{"delete", http.MethodDelete, "/api/batches/" + id, nil},
		{"create-reading", http.MethodPost, "/api/batches/" + id + "/readings", map[string]any{"gravity": 1.0}},
		{"list-readings", http.MethodGet, "/api/batches/" + id + "/readings", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, srv, tc.method, tc.path, tc.body, bobCookies...)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("got %d, want 404", resp.StatusCode)
			}
		})
	}

	// Bob's list is empty.
	resp = doJSON(t, srv, http.MethodGet, "/api/batches", nil, bobCookies...)
	bobList := decodeMap(t, resp)
	if arr, _ := bobList["batches"].([]any); len(arr) != 0 {
		t.Fatalf("bob's list should be empty, got %d", len(arr))
	}
}

// Phase 5 opens cider + wine; beer (Phase 6) and kombucha (Phase 7) stay
// rejected at the API surface because they need flow logic that isn't built
// yet (mash/boil for beer, F1/F2 for kombucha).
func TestBatches_RejectsUnsupportedBrewTypes(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	for _, bt := range []string{"beer", "kombucha"} {
		t.Run(bt, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{
				"name":      "x",
				"brew_type": bt,
			}, cookies...)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestBatches_AcceptsCiderAndWine(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	for _, bt := range []string{"cider", "wine"} {
		t.Run(bt, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{
				"name":      bt + " batch",
				"brew_type": bt,
			}, cookies...)
			if resp.StatusCode != http.StatusCreated {
				t.Fatalf("got %d, want 201", resp.StatusCode)
			}
			created := decodeMap(t, resp)
			if created["brew_type"] != bt {
				t.Errorf("brew_type round-trip: got %v, want %s", created["brew_type"], bt)
			}
		})
	}
}

func TestBatches_CreateValidation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty-name", map[string]any{"name": ""}},
		{"bad-stage", map[string]any{"name": "x", "stage": "bogus"}},
		{"bad-visibility", map[string]any{"name": "x", "visibility": "bogus"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/batches", c.body, cookies...)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestReadings_RequireValue(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")
	resp := doJSON(t, srv, http.MethodPost, "/api/batches", map[string]any{"name": "x"}, cookies...)
	id, _ := decodeMap(t, resp)["id"].(string)

	// At least one of gravity/temperature_c/ph required.
	resp = doJSON(t, srv, http.MethodPost, "/api/batches/"+id+"/readings", map[string]any{
		"notes": "no measurements",
	}, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}
