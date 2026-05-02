package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
//	have      — inventory amount >= ingredient amount (or amount is NULL)
//	short     — inventory exists but amount < ingredient amount
//	missing   — no inventory row matches (with or without unit_mismatch)
//
// shortfall is populated only when status == "short" and both amounts
// are present, expressed in the recipe ingredient's unit (no conversion).
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

	views := make([]inventoryMatchView, 0, len(rows))
	for _, row := range rows {
		views = append(views, buildInventoryMatchView(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

func buildInventoryMatchView(row queries.MatchInventoryForRecipeRow) inventoryMatchView {
	v := inventoryMatchView{
		IngredientID: row.IngredientID.String(),
		Kind:         string(row.IngredientKind),
		Name:         row.IngredientName,
		Amount:       numericPtr(row.IngredientAmount),
		Unit:         textOrEmpty(row.IngredientUnit),
		UnitMismatch: row.HasUnitMismatch,
	}
	if !row.InventoryID.Valid {
		v.Status = "missing"
		return v
	}
	v.InventoryID = row.InventoryID.UUID.String()
	v.InventoryAmount = numericPtr(row.InventoryAmount)
	// "Have it" semantics:
	//   - both amounts present → compare; short if inv < req
	//   - inventory amount missing → assume the brewer has enough (they
	//     entered the row knowing it was on hand); status = have
	//   - recipe amount missing → no shortfall to compute; status = have
	if v.Amount == nil || v.InventoryAmount == nil {
		v.Status = "have"
		return v
	}
	if *v.InventoryAmount < *v.Amount {
		v.Status = "short"
		gap := *v.Amount - *v.InventoryAmount
		v.Shortfall = &gap
		return v
	}
	v.Status = "have"
	return v
}
