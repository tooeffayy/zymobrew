package server_test

import (
	"net/http"
	"testing"

	"zymobrew/internal/config"
)

func TestInventory_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	// Empty list initially.
	resp := doJSON(t, srv, http.MethodGet, "/api/inventory", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	if items, _ := body["items"].([]any); len(items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(items))
	}

	// Create.
	resp = doJSON(t, srv, http.MethodPost, "/api/inventory", map[string]any{
		"kind":   "honey",
		"name":   "Wildflower",
		"amount": 5.0,
		"unit":   "lb",
		"notes":  "local apiary",
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: got %d", resp.StatusCode)
	}
	created := decodeMap(t, resp)
	id, _ := created["id"].(string)
	if id == "" || created["kind"] != "honey" || created["name"] != "Wildflower" {
		t.Fatalf("create response wrong: %+v", created)
	}
	if amt, _ := created["amount"].(float64); amt != 5.0 {
		t.Fatalf("amount round-trip: %v", created["amount"])
	}

	// PATCH amount up.
	resp = doJSON(t, srv, http.MethodPatch, "/api/inventory/"+id, map[string]any{
		"amount": 7.5,
		"notes":  "topped up",
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch: got %d", resp.StatusCode)
	}
	updated := decodeMap(t, resp)
	if amt, _ := updated["amount"].(float64); amt != 7.5 {
		t.Fatalf("amount not updated: %+v", updated)
	}
	if updated["notes"] != "topped up" {
		t.Fatalf("notes not updated: %+v", updated)
	}
	// Untouched fields preserved (COALESCE PATCH).
	if updated["kind"] != "honey" || updated["name"] != "Wildflower" || updated["unit"] != "lb" {
		t.Fatalf("untouched fields drifted: %+v", updated)
	}

	// List shows the row.
	resp = doJSON(t, srv, http.MethodGet, "/api/inventory", nil, cookies...)
	body = decodeMap(t, resp)
	if items, _ := body["items"].([]any); len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Delete.
	resp = doJSON(t, srv, http.MethodDelete, "/api/inventory/"+id, nil, cookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: got %d", resp.StatusCode)
	}
	// Second delete → 404.
	resp = doJSON(t, srv, http.MethodDelete, "/api/inventory/"+id, nil, cookies...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete after delete: got %d", resp.StatusCode)
	}
}

func TestInventory_Validation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"missing-kind", map[string]any{"name": "x"}},
		{"missing-name", map[string]any{"kind": "honey"}},
		{"bad-kind", map[string]any{"kind": "narwhal", "name": "x"}},
		{"name-too-long", map[string]any{"kind": "honey", "name": stringOfLen(201)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/inventory", c.body, cookies...)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestInventory_OwnershipIsolation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	alice := registerHelper(t, srv, "alice")
	bob := registerHelper(t, srv, "bob")

	resp := doJSON(t, srv, http.MethodPost, "/api/inventory", map[string]any{
		"kind": "honey", "name": "Wildflower", "amount": 5.0, "unit": "lb",
	}, alice...)
	id, _ := decodeMap(t, resp)["id"].(string)

	// Bob can't see, patch, or delete Alice's item — all 404.
	for _, c := range []struct {
		name, method, path string
		body               any
	}{
		{"patch", http.MethodPatch, "/api/inventory/" + id, map[string]any{"name": "stolen"}},
		{"delete", http.MethodDelete, "/api/inventory/" + id, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, c.method, c.path, c.body, bob...)
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("got %d, want 404", resp.StatusCode)
			}
		})
	}

	// Bob's list is empty.
	resp = doJSON(t, srv, http.MethodGet, "/api/inventory", nil, bob...)
	if items, _ := decodeMap(t, resp)["items"].([]any); len(items) != 0 {
		t.Fatalf("bob sees alice's items: %d", len(items))
	}
}

func TestInventory_RequireAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/inventory"},
		{http.MethodPost, "/api/inventory"},
		{http.MethodPatch, "/api/inventory/00000000-0000-0000-0000-000000000000"},
		{http.MethodDelete, "/api/inventory/00000000-0000-0000-0000-000000000000"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			resp := doJSON(t, srv, c.method, c.path, map[string]any{"kind": "honey", "name": "x"})
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", resp.StatusCode)
			}
		})
	}
}

// TestInventory_RecipeMatch covers the per-recipe match endpoint —
// the user-facing "do I have what this recipe needs" lookup. Exercises
// every status (have/short/missing) plus the unit_mismatch hint and
// the unit-conversion paths added in the conversion patch:
//   - cross-unit have via conversion (recipe g, inventory oz)
//   - sum across multiple inventory rows in different compatible units
//   - true unit_mismatch (recipe asks mass, inventory holds count units)
//   - inventory amount NULL → "have, quantity unknown"
func TestInventory_RecipeMatch(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerHelper(t, srv, "alice")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes", map[string]any{
		"name":      "Mead Test",
		"brew_type": "mead",
		"ingredients": []map[string]any{
			{"kind": "honey", "name": "Wildflower", "amount": 3.0, "unit": "lb", "sort_order": 0},
			{"kind": "yeast", "name": "K1-V1116", "amount": 5.0, "unit": "g", "sort_order": 1},
			{"kind": "spice", "name": "Cinnamon", "amount": 1.0, "unit": "stick", "sort_order": 2},
			{"kind": "fruit", "name": "Orange Zest", "amount": 30.0, "unit": "g", "sort_order": 3},
			{"kind": "honey", "name": "Clover", "amount": 1500.0, "unit": "g", "sort_order": 4},
			{"kind": "spice", "name": "Star Anise", "amount": 5.0, "unit": "g", "sort_order": 5},
			{"kind": "nutrient", "name": "Fermaid-O", "amount": 2.0, "unit": "g", "sort_order": 6},
		},
	}, cookies...)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create recipe: got %d", resp.StatusCode)
	}
	recipeID, _ := decodeMap(t, resp)["id"].(string)

	// Inventory layout exercising each path:
	//   wildflower    — 5 lb (have, exact-unit match)
	//   K1-V1116      — 2 g  (short by 3 g)
	//   cinnamon      — none (missing)
	//   orange zest   — 30 oz (have via oz→g conversion: ~850 g >> 30 g)
	//   clover honey  — 1 lb + 600 g (have via cross-unit sum: 453.6 + 600
	//                                = 1053 g, short by ~447 g — actually
	//                                short, so this exercises sum-then-short)
	//   star anise    — 2 sticks (true unit_mismatch — recipe wants g,
	//                              inventory only in count units)
	//   Fermaid-O     — quantity unknown (NULL amount → have)
	for _, item := range []map[string]any{
		{"kind": "honey", "name": "wildflower", "amount": 5.0, "unit": "lb"}, // case-insensitive name match
		{"kind": "yeast", "name": "K1-V1116", "amount": 2.0, "unit": "g"},
		{"kind": "fruit", "name": "Orange Zest", "amount": 30.0, "unit": "oz"},
		{"kind": "honey", "name": "Clover", "amount": 1.0, "unit": "lb"},
		{"kind": "honey", "name": "Clover", "amount": 600.0, "unit": "g"},
		{"kind": "spice", "name": "Star Anise", "amount": 2.0, "unit": "stick"},
		{"kind": "nutrient", "name": "Fermaid-O"}, // no amount, no unit
	} {
		r := doJSON(t, srv, http.MethodPost, "/api/inventory", item, cookies...)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("create inventory %v: got %d", item, r.StatusCode)
		}
	}

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+recipeID+"/inventory-match", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("match: got %d", resp.StatusCode)
	}
	body := decodeMap(t, resp)
	itemsAny, _ := body["items"].([]any)
	if len(itemsAny) != 7 {
		t.Fatalf("expected 7 match rows, got %d", len(itemsAny))
	}

	byName := map[string]map[string]any{}
	for _, it := range itemsAny {
		m, _ := it.(map[string]any)
		name, _ := m["name"].(string)
		byName[name] = m
	}

	// Wildflower honey: have via exact-unit match
	if byName["Wildflower"]["status"] != "have" {
		t.Errorf("Wildflower: want have, got %v", byName["Wildflower"]["status"])
	}
	if _, ok := byName["Wildflower"]["shortfall"]; ok {
		t.Errorf("Wildflower: shortfall should be omitted on have")
	}

	// K1-V1116: short by 3 g
	if byName["K1-V1116"]["status"] != "short" {
		t.Errorf("K1-V1116: want short, got %v", byName["K1-V1116"]["status"])
	}
	if sf, _ := byName["K1-V1116"]["shortfall"].(float64); sf != 3.0 {
		t.Errorf("K1-V1116 shortfall: want 3, got %v", byName["K1-V1116"]["shortfall"])
	}

	// Cinnamon: missing
	if byName["Cinnamon"]["status"] != "missing" {
		t.Errorf("Cinnamon: want missing, got %v", byName["Cinnamon"]["status"])
	}
	if mm, _ := byName["Cinnamon"]["unit_mismatch"].(bool); mm {
		t.Errorf("Cinnamon: unit_mismatch should be false (no inventory at all)")
	}

	// Orange Zest: HAVE via oz → g conversion (30 oz ≈ 850 g >> 30 g)
	if byName["Orange Zest"]["status"] != "have" {
		t.Errorf("Orange Zest: want have (cross-unit conversion), got %v", byName["Orange Zest"]["status"])
	}
	if mm, _ := byName["Orange Zest"]["unit_mismatch"].(bool); mm {
		t.Errorf("Orange Zest: unit_mismatch should be false now that oz→g converts")
	}
	if invAmt, _ := byName["Orange Zest"]["inventory_amount"].(float64); invAmt < 800 || invAmt > 900 {
		t.Errorf("Orange Zest inventory_amount (in g): want ~850, got %v", invAmt)
	}

	// Clover honey: 1 lb + 600 g = ~1053.6 g, recipe wants 1500 g → SHORT by ~446 g
	if byName["Clover"]["status"] != "short" {
		t.Errorf("Clover: want short (cross-unit sum), got %v", byName["Clover"]["status"])
	}
	invAmt, _ := byName["Clover"]["inventory_amount"].(float64)
	if invAmt < 1050 || invAmt > 1060 {
		t.Errorf("Clover inventory_amount (sum in g): want ~1053.6, got %v", invAmt)
	}
	gap, _ := byName["Clover"]["shortfall"].(float64)
	if gap < 440 || gap > 450 {
		t.Errorf("Clover shortfall (in g): want ~446.4, got %v", gap)
	}

	// Star Anise: missing + unit_mismatch (recipe g, inventory only in stick)
	if byName["Star Anise"]["status"] != "missing" {
		t.Errorf("Star Anise: want missing, got %v", byName["Star Anise"]["status"])
	}
	if mm, _ := byName["Star Anise"]["unit_mismatch"].(bool); !mm {
		t.Errorf("Star Anise: unit_mismatch should be true (recipe g, inventory stick)")
	}

	// Fermaid-O: have, quantity unknown
	if byName["Fermaid-O"]["status"] != "have" {
		t.Errorf("Fermaid-O: want have (NULL inventory amount), got %v", byName["Fermaid-O"]["status"])
	}
	if _, ok := byName["Fermaid-O"]["inventory_amount"]; ok {
		t.Errorf("Fermaid-O: inventory_amount should be omitted (we couldn't measure)")
	}
}

// TestInventory_RecipeMatch_PrivateRecipeReturns404 — non-owners get a
// 404 on private recipes (matches the rest of the recipe surface). The
// match endpoint is auth-required; we verify the visibility check too.
func TestInventory_RecipeMatch_PrivateRecipeReturns404(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	alice := registerHelper(t, srv, "alice")
	bob := registerHelper(t, srv, "bob")

	resp := doJSON(t, srv, http.MethodPost, "/api/recipes", map[string]any{
		"name":       "Secret Mead",
		"brew_type":  "mead",
		"visibility": "private",
		"ingredients": []map[string]any{
			{"kind": "honey", "name": "Wildflower", "amount": 3.0, "unit": "lb"},
		},
	}, alice...)
	id, _ := decodeMap(t, resp)["id"].(string)

	resp = doJSON(t, srv, http.MethodGet, "/api/recipes/"+id+"/inventory-match", nil, bob...)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bob match private recipe: got %d, want 404", resp.StatusCode)
	}
}

func stringOfLen(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
