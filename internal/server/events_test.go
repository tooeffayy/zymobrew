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

	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"create", http.MethodPost, "/api/batches/" + id + "/events", map[string]any{"kind": "note"}},
		{"list", http.MethodGet, "/api/batches/" + id + "/events", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, srv, tc.method, tc.path, tc.body, bobCookies...)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("got %d, want 404", resp.StatusCode)
			}
		})
	}
}
