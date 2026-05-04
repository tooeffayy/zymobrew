package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/calc"
	"zymobrew/internal/queries"
)

const (
	maxInventoryName  = 200
	maxInventoryUnit  = 32
	maxInventoryNotes = 2 * 1024
)

// --- DTOs ------------------------------------------------------------------

type inventoryItemView struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Amount    *float64  `json:"amount,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	Notes     string    `json:"notes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toInventoryView(i queries.InventoryItem) inventoryItemView {
	return inventoryItemView{
		ID:        i.ID.String(),
		Kind:      string(i.Kind),
		Name:      i.Name,
		Amount:    numericPtr(i.Amount),
		Unit:      textOrEmpty(i.Unit),
		Notes:     textOrEmpty(i.Notes),
		CreatedAt: i.CreatedAt.Time,
		UpdatedAt: i.UpdatedAt.Time,
	}
}

// --- list / get / create / update / delete --------------------------------

type createInventoryRequest struct {
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Amount *float64 `json:"amount,omitempty"`
	Unit   string   `json:"unit,omitempty"`
	Notes  string   `json:"notes,omitempty"`
}

func (s *Server) handleListInventory(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	rows, err := s.queries.ListInventoryForUser(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]inventoryItemView, 0, len(rows))
	for _, row := range rows {
		views = append(views, toInventoryView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func (s *Server) handleCreateInventory(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	var req createInventoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if errMsg := validateInventoryFields(req.Kind, req.Name, req.Unit, req.Notes); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	row, err := s.queries.CreateInventoryItem(r.Context(), queries.CreateInventoryItemParams{
		UserID: user.ID,
		Kind:   queries.IngredientKind(req.Kind),
		Name:   req.Name,
		Amount: floatToNumeric(req.Amount),
		Unit:   optText(req.Unit),
		Notes:  optText(req.Notes),
	})
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid kind value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, toInventoryView(row))
}

type updateInventoryRequest struct {
	Kind   *string  `json:"kind,omitempty"`
	Name   *string  `json:"name,omitempty"`
	Amount *float64 `json:"amount,omitempty"`
	Unit   *string  `json:"unit,omitempty"`
	Notes  *string  `json:"notes,omitempty"`
}

func (s *Server) handleUpdateInventory(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	var req updateInventoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	// Pull the current row first so PATCH can validate the resulting shape
	// (e.g. name length on the patched value, not the missing one) and so
	// 404s short-circuit before we even build the params.
	existing, err := s.queries.GetInventoryItemForUser(r.Context(), queries.GetInventoryItemForUserParams{
		ID: id, UserID: user.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}

	kind := string(existing.Kind)
	name := existing.Name
	unit := textOrEmpty(existing.Unit)
	notes := textOrEmpty(existing.Notes)
	if req.Kind != nil {
		kind = *req.Kind
	}
	if req.Name != nil {
		name = *req.Name
	}
	if req.Unit != nil {
		unit = *req.Unit
	}
	if req.Notes != nil {
		notes = *req.Notes
	}
	if errMsg := validateInventoryFields(kind, name, unit, notes); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	params := queries.UpdateInventoryItemParams{ID: id, UserID: user.ID}
	if req.Kind != nil {
		params.Kind = queries.NullIngredientKind{IngredientKind: queries.IngredientKind(*req.Kind), Valid: true}
	}
	if req.Name != nil {
		params.Name = pgtype.Text{String: *req.Name, Valid: true}
	}
	if req.Amount != nil {
		params.Amount = floatToNumeric(req.Amount)
	}
	if req.Unit != nil {
		params.Unit = pgtype.Text{String: *req.Unit, Valid: true}
	}
	if req.Notes != nil {
		params.Notes = pgtype.Text{String: *req.Notes, Valid: true}
	}

	row, err := s.queries.UpdateInventoryItem(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid kind value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}
	writeJSON(w, http.StatusOK, toInventoryView(row))
}

func (s *Server) handleDeleteInventory(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	n, err := s.queries.DeleteInventoryItem(r.Context(), queries.DeleteInventoryItemParams{
		ID: id, UserID: user.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if n == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// validateInventoryFields enforces the same caps as the recipe-ingredient
// surface so an inventory entry round-trips into a recipe match cleanly.
// Kind validity beyond "non-empty" is enforced by the ENUM cast at write
// time — same posture as recipes.go.
func validateInventoryFields(kind, name, unit, notes string) string {
	if kind == "" {
		return "kind required"
	}
	if name == "" || len(name) > maxInventoryName {
		return "name required (max 200 chars)"
	}
	if len(unit) > maxInventoryUnit {
		return "unit too long"
	}
	if len(notes) > maxInventoryNotes {
		return "notes too long"
	}
	return ""
}

// --- recipe match ---------------------------------------------------------

// inventoryMatchView is the per-ingredient row in a recipe's inventory
// match. status is one of:
//
//	have      — inventory (after unit conversion + aggregation) covers
//	            the recipe's amount, or the recipe doesn't specify one.
//	short     — inventory exists in convertible units but the converted
//	            sum falls below the recipe amount.
//	missing   — no inventory rows match kind + name, or every match is
//	            in a non-convertible unit (in which case unit_mismatch
//	            is true).
//
// inventory_amount is the aggregated total in the recipe's unit when
// at least one convertible inventory row exists. shortfall is populated
// only when status == "short" and is also in the recipe's unit.
type inventoryMatchView struct {
	IngredientID    string   `json:"ingredient_id"`
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	Amount          *float64 `json:"amount,omitempty"`
	Unit            string   `json:"unit,omitempty"`
	Status          string   `json:"status"`
	InventoryID     string   `json:"inventory_id,omitempty"`
	InventoryAmount *float64 `json:"inventory_amount,omitempty"`
	Shortfall       *float64 `json:"shortfall,omitempty"`
	UnitMismatch    bool     `json:"unit_mismatch,omitempty"`
}

func (s *Server) handleRecipeInventoryMatch(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	recipeID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid recipe id"})
		return
	}
	// Visibility check — private recipes return 404 to non-owners (matches
	// the rest of the recipe surface). Public/unlisted are visible to any
	// authenticated user, but the match itself is per-user inventory.
	recipe, err := s.queries.GetRecipeByID(r.Context(), recipeID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	if !recipeVisibleTo(recipe, user) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	rows, err := s.queries.MatchInventoryForRecipe(r.Context(), queries.MatchInventoryForRecipeParams{
		UserID:   user.ID,
		RecipeID: recipeID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "match failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": aggregateInventoryMatch(rows)})
}

// aggregateInventoryMatch reduces the (ingredient × inventory) row set
// returned by MatchInventoryForRecipe into one view per recipe ingredient,
// applying unit conversion + summing across convertible inventory rows.
//
// Rules per ingredient:
//   - No matching inventory rows at all → missing.
//   - Recipe has no amount → any matching row counts as have.
//   - Some inventory row has NULL amount → have (we know they have some
//     of it; can't measure the shortfall, don't pretend to).
//   - Convert each compatible-unit inventory row into the recipe's unit
//     and sum. Compare against the recipe amount → have / short.
//   - When *every* matching row uses an incompatible unit (e.g., recipe
//     wants "g", inventory has "stick"), there's nothing to sum →
//     missing + unit_mismatch=true.
func aggregateInventoryMatch(rows []queries.MatchInventoryForRecipeRow) []inventoryMatchView {
	// Group rows by ingredient_id, preserving the SQL's ingredient order.
	type bucket struct {
		ingredient queries.MatchInventoryForRecipeRow
		matches    []queries.MatchInventoryForRecipeRow
	}
	order := []uuid.UUID{}
	buckets := map[uuid.UUID]*bucket{}
	for _, row := range rows {
		b, ok := buckets[row.IngredientID]
		if !ok {
			b = &bucket{ingredient: row}
			buckets[row.IngredientID] = b
			order = append(order, row.IngredientID)
		}
		if row.InventoryID.Valid {
			b.matches = append(b.matches, row)
		}
	}

	views := make([]inventoryMatchView, 0, len(order))
	for _, id := range order {
		b := buckets[id]
		views = append(views, classifyMatch(b.ingredient, b.matches))
	}
	return views
}

func classifyMatch(ingredient queries.MatchInventoryForRecipeRow, matches []queries.MatchInventoryForRecipeRow) inventoryMatchView {
	v := inventoryMatchView{
		IngredientID: ingredient.IngredientID.String(),
		Kind:         string(ingredient.IngredientKind),
		Name:         ingredient.IngredientName,
		Amount:       numericPtr(ingredient.IngredientAmount),
		Unit:         textOrEmpty(ingredient.IngredientUnit),
	}

	if len(matches) == 0 {
		v.Status = "missing"
		return v
	}

	// Recipe specifies no amount → "I just need some of this." Any
	// matching kind+name row qualifies. Pick the first match's id so
	// the UI can deep-link if desired.
	if v.Amount == nil {
		v.Status = "have"
		v.InventoryID = matches[0].InventoryID.UUID.String()
		return v
	}

	// Walk matches; convert to the recipe's unit when possible. NULL
	// amounts short-circuit to "have" — we know the brewer has some,
	// they just didn't measure it.
	var sum float64
	convertible := 0
	for _, m := range matches {
		invAmt := numericPtr(m.InventoryAmount)
		if invAmt == nil {
			v.Status = "have"
			v.InventoryID = m.InventoryID.UUID.String()
			return v
		}
		converted, ok := calc.Convert(*invAmt, textOrEmpty(m.InventoryUnit), v.Unit)
		if !ok {
			continue
		}
		sum += converted
		convertible++
	}

	if convertible == 0 {
		// Inventory rows exist but none can be compared in the recipe's
		// unit — surface the gap so the UI can flag it instead of
		// silently calling it missing.
		v.Status = "missing"
		v.UnitMismatch = true
		return v
	}

	v.InventoryAmount = &sum
	// Use the first convertible match's id as the canonical "this is
	// what we matched against" pointer. Aggregation across multiple
	// rows means there isn't one true id; this is good enough for UI
	// linking and tests assert on it deterministically (server orders
	// by inv.created_at ASC).
	for _, m := range matches {
		if _, ok := calc.Convert(1, textOrEmpty(m.InventoryUnit), v.Unit); ok {
			v.InventoryID = m.InventoryID.UUID.String()
			break
		}
	}

	if sum < *v.Amount {
		gap := *v.Amount - sum
		v.Shortfall = &gap
		v.Status = "short"
		return v
	}
	v.Status = "have"
	return v
}
