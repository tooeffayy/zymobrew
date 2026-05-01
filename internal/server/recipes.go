package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/queries"
)

const (
	maxRecipeName  = 200
	maxRecipeDesc  = 10 * 1024
	maxRecipeStyle = 100
	maxIngredients = 50
	maxIngredName  = 200
	maxCommentBody = 10 * 1024
)

// --- view types -----------------------------------------------------------

type ingredientView struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Amount    *float64        `json:"amount,omitempty"`
	Unit      string          `json:"unit,omitempty"`
	SortOrder int32           `json:"sort_order"`
	Details   json.RawMessage `json:"details"`
}

func toIngredientView(i queries.RecipeIngredient) ingredientView {
	details := json.RawMessage(i.Details)
	if len(details) == 0 {
		details = json.RawMessage("{}")
	}
	return ingredientView{
		ID:        i.ID.String(),
		Kind:      string(i.Kind),
		Name:      i.Name,
		Amount:    numericPtr(i.Amount),
		Unit:      textOrEmpty(i.Unit),
		SortOrder: i.SortOrder,
		Details:   details,
	}
}

type recipeView struct {
	ID             string           `json:"id"`
	AuthorID       string           `json:"author_id"`
	ParentID       *string          `json:"parent_id,omitempty"`
	RevisionNumber int32            `json:"revision_number"`
	RevisionCount  int32            `json:"revision_count"`
	ForkCount      int32            `json:"fork_count"`
	BrewType       string           `json:"brew_type"`
	Style          string           `json:"style,omitempty"`
	Name           string           `json:"name"`
	Description    string           `json:"description,omitempty"`
	TargetOG       *float64         `json:"target_og,omitempty"`
	TargetFG       *float64         `json:"target_fg,omitempty"`
	TargetABV      *float64         `json:"target_abv,omitempty"`
	BatchSizeL     *float64         `json:"batch_size_l,omitempty"`
	Visibility     string           `json:"visibility"`
	Message        string           `json:"message,omitempty"`
	Ingredients    []ingredientView `json:"ingredients"`
	CreatedAt      string           `json:"created_at"`
	UpdatedAt      string           `json:"updated_at"`
}

func toRecipeView(r queries.Recipe, rev queries.RecipeRevision, ings []queries.RecipeIngredient) recipeView {
	var parentID *string
	if r.ParentID.Valid {
		s := r.ParentID.UUID.String()
		parentID = &s
	}
	ingViews := make([]ingredientView, 0, len(ings))
	for _, ing := range ings {
		ingViews = append(ingViews, toIngredientView(ing))
	}
	return recipeView{
		ID:             r.ID.String(),
		AuthorID:       r.AuthorID.String(),
		ParentID:       parentID,
		RevisionNumber: rev.RevisionNumber,
		RevisionCount:  r.RevisionCount,
		ForkCount:      r.ForkCount,
		BrewType:       string(r.BrewType),
		Style:          textOrEmpty(r.Style),
		Name:           r.Name,
		Description:    textOrEmpty(r.Description),
		TargetOG:       numericPtr(r.TargetOg),
		TargetFG:       numericPtr(r.TargetFg),
		TargetABV:      numericPtr(r.TargetAbv),
		BatchSizeL:     numericPtr(r.BatchSizeL),
		Visibility:     string(r.Visibility),
		Message:        textOrEmpty(rev.Message),
		Ingredients:    ingViews,
		CreatedAt:      r.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:      r.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
}

type recipeListItemView struct {
	ID            string   `json:"id"`
	AuthorID      string   `json:"author_id"`
	ParentID      *string  `json:"parent_id,omitempty"`
	RevisionCount int32    `json:"revision_count"`
	ForkCount     int32    `json:"fork_count"`
	BrewType      string   `json:"brew_type"`
	Style         string   `json:"style,omitempty"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	TargetOG      *float64 `json:"target_og,omitempty"`
	TargetFG      *float64 `json:"target_fg,omitempty"`
	TargetABV     *float64 `json:"target_abv,omitempty"`
	BatchSizeL    *float64 `json:"batch_size_l,omitempty"`
	Visibility    string   `json:"visibility"`
	UpdatedAt     string   `json:"updated_at"`
}

func toRecipeListItem(r queries.Recipe) recipeListItemView {
	var parentID *string
	if r.ParentID.Valid {
		s := r.ParentID.UUID.String()
		parentID = &s
	}
	return recipeListItemView{
		ID:            r.ID.String(),
		AuthorID:      r.AuthorID.String(),
		ParentID:      parentID,
		RevisionCount: r.RevisionCount,
		ForkCount:     r.ForkCount,
		BrewType:      string(r.BrewType),
		Style:         textOrEmpty(r.Style),
		Name:          r.Name,
		Description:   textOrEmpty(r.Description),
		TargetOG:      numericPtr(r.TargetOg),
		TargetFG:      numericPtr(r.TargetFg),
		TargetABV:     numericPtr(r.TargetAbv),
		BatchSizeL:    numericPtr(r.BatchSizeL),
		Visibility:    string(r.Visibility),
		UpdatedAt:     r.UpdatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
}

type revisionSummaryView struct {
	ID             string `json:"id"`
	RevisionNumber int32  `json:"revision_number"`
	AuthorID       string `json:"author_id"`
	Message        string `json:"message,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type revisionDetailView struct {
	ID             string          `json:"id"`
	RevisionNumber int32           `json:"revision_number"`
	AuthorID       string          `json:"author_id"`
	Message        string          `json:"message,omitempty"`
	Name           string          `json:"name"`
	Style          string          `json:"style,omitempty"`
	Description    string          `json:"description,omitempty"`
	TargetOG       *float64        `json:"target_og,omitempty"`
	TargetFG       *float64        `json:"target_fg,omitempty"`
	TargetABV      *float64        `json:"target_abv,omitempty"`
	BatchSizeL     *float64        `json:"batch_size_l,omitempty"`
	Ingredients    json.RawMessage `json:"ingredients"`
	CreatedAt      string          `json:"created_at"`
}

func toRevisionDetail(rev queries.RecipeRevision) revisionDetailView {
	ings := json.RawMessage(rev.Ingredients)
	if len(ings) == 0 {
		ings = json.RawMessage("[]")
	}
	return revisionDetailView{
		ID:             rev.ID.String(),
		RevisionNumber: rev.RevisionNumber,
		AuthorID:       rev.AuthorID.String(),
		Message:        textOrEmpty(rev.Message),
		Name:           rev.Name,
		Style:          textOrEmpty(rev.Style),
		Description:    textOrEmpty(rev.Description),
		TargetOG:       numericPtr(rev.TargetOg),
		TargetFG:       numericPtr(rev.TargetFg),
		TargetABV:      numericPtr(rev.TargetAbv),
		BatchSizeL:     numericPtr(rev.BatchSizeL),
		Ingredients:    ings,
		CreatedAt:      rev.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
}

// --- ingredient snapshot --------------------------------------------------

type ingredientSnap struct {
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Amount    *float64        `json:"amount,omitempty"`
	Unit      string          `json:"unit,omitempty"`
	SortOrder int32           `json:"sort_order"`
	Details   json.RawMessage `json:"details,omitempty"`
}

func buildSnapshot(ings []queries.RecipeIngredient) []byte {
	snaps := make([]ingredientSnap, 0, len(ings))
	for _, ing := range ings {
		snap := ingredientSnap{
			Kind:      string(ing.Kind),
			Name:      ing.Name,
			Amount:    numericPtr(ing.Amount),
			Unit:      textOrEmpty(ing.Unit),
			SortOrder: ing.SortOrder,
		}
		if len(ing.Details) > 0 {
			snap.Details = json.RawMessage(ing.Details)
		}
		snaps = append(snaps, snap)
	}
	b, _ := json.Marshal(snaps)
	return b
}

// --- request types --------------------------------------------------------

type ingredientInput struct {
	Kind      string          `json:"kind"`
	Name      string          `json:"name"`
	Amount    *float64        `json:"amount,omitempty"`
	Unit      string          `json:"unit,omitempty"`
	SortOrder int32           `json:"sort_order"`
	Details   json.RawMessage `json:"details,omitempty"`
}

type createRecipeRequest struct {
	Name        string            `json:"name"`
	BrewType    string            `json:"brew_type"`
	Style       string            `json:"style,omitempty"`
	Description string            `json:"description,omitempty"`
	TargetOG    *float64          `json:"target_og,omitempty"`
	TargetFG    *float64          `json:"target_fg,omitempty"`
	TargetABV   *float64          `json:"target_abv,omitempty"`
	BatchSizeL  *float64          `json:"batch_size_l,omitempty"`
	Visibility  string            `json:"visibility,omitempty"`
	Message     string            `json:"message,omitempty"`
	Ingredients []ingredientInput `json:"ingredients"`
}

type forkRecipeRequest struct {
	Name    *string `json:"name,omitempty"`
	Message *string `json:"message,omitempty"`
}

type updateRecipeRequest struct {
	Name        *string            `json:"name,omitempty"`
	Style       *string            `json:"style,omitempty"`
	Description *string            `json:"description,omitempty"`
	TargetOG    *float64           `json:"target_og,omitempty"`
	TargetFG    *float64           `json:"target_fg,omitempty"`
	TargetABV   *float64           `json:"target_abv,omitempty"`
	BatchSizeL  *float64           `json:"batch_size_l,omitempty"`
	Visibility  *string            `json:"visibility,omitempty"`
	Message     *string            `json:"message,omitempty"`
	Ingredients *[]ingredientInput `json:"ingredients,omitempty"`
}

type commentView struct {
	ID             string `json:"id"`
	RecipeID       string `json:"recipe_id"`
	AuthorID       string `json:"author_id"`
	AuthorUsername string `json:"author_username"`
	Body           string `json:"body"`
	CreatedAt      string `json:"created_at"`
}

type createCommentRequest struct {
	Body string `json:"body"`
}

// --- validation helpers ---------------------------------------------------

func validateIngredients(ings []ingredientInput) string {
	if len(ings) > maxIngredients {
		return "too many ingredients (max 50)"
	}
	for i, ing := range ings {
		if ing.Kind == "" {
			return "ingredient[" + strconv.Itoa(i) + "].kind required"
		}
		if ing.Name == "" || len(ing.Name) > maxIngredName {
			return "ingredient[" + strconv.Itoa(i) + "].name required (max 200 chars)"
		}
		if len(ing.Details) > 0 && (!json.Valid(ing.Details) || ing.Details[0] != '{') {
			return "ingredient[" + strconv.Itoa(i) + "].details must be a JSON object"
		}
	}
	return ""
}

// --- recipe visibility check ---------------------------------------------

// recipeVisibleTo returns true if the recipe is visible to the requesting user.
// Private recipes are only visible to their author.
func recipeVisibleTo(r queries.Recipe, user *queries.User) bool {
	if r.Visibility != queries.VisibilityPrivate {
		return true
	}
	return user != nil && r.AuthorID == user.ID
}

// --- transaction helper ---------------------------------------------------

// insertIngredients inserts all ingredients and returns the rows created.
func (s *Server) insertIngredients(ctx context.Context, qtx *queries.Queries, recipeID uuid.UUID, ings []ingredientInput) ([]queries.RecipeIngredient, error) {
	rows := make([]queries.RecipeIngredient, 0, len(ings))
	for _, ing := range ings {
		details := []byte("{}")
		if len(ing.Details) > 0 {
			details = ing.Details
		}
		row, err := qtx.CreateRecipeIngredient(ctx, queries.CreateRecipeIngredientParams{
			RecipeID:  recipeID,
			Kind:      queries.IngredientKind(ing.Kind),
			Name:      ing.Name,
			Amount:    floatToNumeric(ing.Amount),
			Unit:      optText(ing.Unit),
			SortOrder: ing.SortOrder,
			Details:   details,
		})
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// --- handlers -------------------------------------------------------------

func (s *Server) handleCreateRecipe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	var req createRecipeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Name == "" || len(req.Name) > maxRecipeName {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required (max 200 chars)"})
		return
	}
	if len(req.Description) > maxRecipeDesc {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description too long"})
		return
	}
	if len(req.Style) > maxRecipeStyle {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "style too long (max 100 chars)"})
		return
	}
	if req.BrewType == "" {
		req.BrewType = defaultBrewType
	}
	if _, ok := allowedBrewTypes[req.BrewType]; !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": unsupportedBrewTypeMsg})
		return
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}
	if errMsg := validateIngredients(req.Ingredients); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	defer tx.Rollback(r.Context())
	qtx := s.queries.WithTx(tx)

	recipe, err := qtx.CreateRecipeDraft(r.Context(), queries.CreateRecipeDraftParams{
		AuthorID:    user.ID,
		BrewType:    queries.BrewType(req.BrewType),
		Name:        req.Name,
		Style:       optText(req.Style),
		Description: optText(req.Description),
		TargetOg:    floatToNumeric(req.TargetOG),
		TargetFg:    floatToNumeric(req.TargetFG),
		TargetAbv:   floatToNumeric(req.TargetABV),
		BatchSizeL:  floatToNumeric(req.BatchSizeL),
		Visibility:  queries.Visibility(req.Visibility),
	})
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid visibility value"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	ingRows, err := s.insertIngredients(r.Context(), qtx, recipe.ID, req.Ingredients)
	if err != nil {
		if isInvalidTextRepresentation(err) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ingredient kind"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	rev, err := qtx.CreateRecipeRevision(r.Context(), queries.CreateRecipeRevisionParams{
		RecipeID:       recipe.ID,
		RevisionNumber: 1,
		AuthorID:       user.ID,
		Message:        optText(req.Message),
		Name:           recipe.Name,
		Style:          recipe.Style,
		Description:    recipe.Description,
		TargetOg:       recipe.TargetOg,
		TargetFg:       recipe.TargetFg,
		TargetAbv:      recipe.TargetAbv,
		BatchSizeL:     recipe.BatchSizeL,
		Ingredients:    buildSnapshot(ingRows),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	recipe, err = qtx.SetRecipeRevision(r.Context(), queries.SetRecipeRevisionParams{
		ID:                recipe.ID,
		CurrentRevisionID: uuid.NullUUID{UUID: rev.ID, Valid: true},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, toRecipeView(recipe, rev, ingRows))
}

func (s *Server) handleGetRecipe(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}

	viewer, _ := userFromContext(r.Context())
	if !recipeVisibleTo(recipe, viewer) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	if !recipe.CurrentRevisionID.Valid {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "recipe has no revision"})
		return
	}
	rev, err := s.queries.GetRevisionByID(r.Context(), recipe.CurrentRevisionID.UUID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	ings, err := s.queries.ListRecipeIngredients(r.Context(), recipe.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	writeJSON(w, http.StatusOK, toRecipeView(recipe, rev, ings))
}

func (s *Server) handleListRecipes(w http.ResponseWriter, r *http.Request) {
	p, ok := parseListPagination(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListPublicRecipes(r.Context(), queries.ListPublicRecipesParams{
		CursorTs: p.CursorTs,
		CursorID: p.CursorID,
		LimitN:   p.Limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]recipeListItemView, 0, len(rows))
	for _, r := range rows {
		views = append(views, toRecipeListItem(r))
	}
	next := nextCursor(len(rows), p.Limit, func() (time.Time, uuid.UUID) {
		last := rows[len(rows)-1]
		return last.UpdatedAt.Time, last.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"recipes": views, "next_cursor": nullableCursor(next)})
}

func (s *Server) handleListMyRecipes(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	p, ok := parseListPagination(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListRecipesForAuthor(r.Context(), queries.ListRecipesForAuthorParams{
		AuthorID: user.ID,
		CursorTs: p.CursorTs,
		CursorID: p.CursorID,
		LimitN:   p.Limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
		return
	}
	views := make([]recipeListItemView, 0, len(rows))
	for _, r := range rows {
		views = append(views, toRecipeListItem(r))
	}
	next := nextCursor(len(rows), p.Limit, func() (time.Time, uuid.UUID) {
		last := rows[len(rows)-1]
		return last.UpdatedAt.Time, last.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"recipes": views, "next_cursor": nullableCursor(next)})
}

func (s *Server) handleUpdateRecipe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	cur, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	if cur.AuthorID != user.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req updateRecipeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Name != nil && (*req.Name == "" || len(*req.Name) > maxRecipeName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid name"})
		return
	}
	if req.Description != nil && len(*req.Description) > maxRecipeDesc {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description too long"})
		return
	}
	if req.Style != nil && len(*req.Style) > maxRecipeStyle {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "style too long (max 100 chars)"})
		return
	}
	if req.Ingredients != nil {
		if errMsg := validateIngredients(*req.Ingredients); errMsg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
			return
		}
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}
	defer tx.Rollback(r.Context())
	qtx := s.queries.WithTx(tx)

	var nameParam pgtype.Text
	if req.Name != nil {
		nameParam = pgtype.Text{String: *req.Name, Valid: true}
	}
	var styleParam pgtype.Text
	if req.Style != nil {
		styleParam = pgtype.Text{String: *req.Style, Valid: true}
	}
	var descParam pgtype.Text
	if req.Description != nil {
		descParam = pgtype.Text{String: *req.Description, Valid: true}
	}
	var visParam queries.NullVisibility
	if req.Visibility != nil {
		visParam = queries.NullVisibility{Visibility: queries.Visibility(*req.Visibility), Valid: true}
	}

	recipe, err := qtx.UpdateRecipeMeta(r.Context(), queries.UpdateRecipeMetaParams{
		ID:          id,
		AuthorID:    user.ID,
		Name:        nameParam,
		Style:       styleParam,
		Description: descParam,
		TargetOg:    floatToNumeric(req.TargetOG),
		TargetFg:    floatToNumeric(req.TargetFG),
		TargetAbv:   floatToNumeric(req.TargetABV),
		BatchSizeL:  floatToNumeric(req.BatchSizeL),
		Visibility:  visParam,
	})
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case isInvalidTextRepresentation(err):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid visibility value"})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		}
		return
	}

	var ingRows []queries.RecipeIngredient
	if req.Ingredients != nil {
		if err := qtx.DeleteRecipeIngredients(r.Context(), id); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		ingRows, err = s.insertIngredients(r.Context(), qtx, id, *req.Ingredients)
		if err != nil {
			if isInvalidTextRepresentation(err) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ingredient kind"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
	} else {
		ingRows, err = qtx.ListRecipeIngredients(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
	}

	var msgParam pgtype.Text
	if req.Message != nil {
		msgParam = pgtype.Text{String: *req.Message, Valid: true}
	}

	rev, err := qtx.CreateRecipeRevision(r.Context(), queries.CreateRecipeRevisionParams{
		RecipeID:       recipe.ID,
		RevisionNumber: recipe.RevisionCount + 1,
		AuthorID:       user.ID,
		Message:        msgParam,
		Name:           recipe.Name,
		Style:          recipe.Style,
		Description:    recipe.Description,
		TargetOg:       recipe.TargetOg,
		TargetFg:       recipe.TargetFg,
		TargetAbv:      recipe.TargetAbv,
		BatchSizeL:     recipe.BatchSizeL,
		Ingredients:    buildSnapshot(ingRows),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	recipe, err = qtx.SetRecipeRevision(r.Context(), queries.SetRecipeRevisionParams{
		ID:                recipe.ID,
		CurrentRevisionID: uuid.NullUUID{UUID: rev.ID, Valid: true},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}
	writeJSON(w, http.StatusOK, toRecipeView(recipe, rev, ingRows))
}

func (s *Server) handleDeleteRecipe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	rows, err := s.queries.DeleteRecipe(r.Context(), queries.DeleteRecipeParams{
		ID: id, AuthorID: user.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if rows == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	viewer, _ := userFromContext(r.Context())
	if !recipeVisibleTo(recipe, viewer) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	revisions, err := s.queries.ListRecipeRevisions(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	views := make([]revisionSummaryView, 0, len(revisions))
	for _, rev := range revisions {
		views = append(views, revisionSummaryView{
			ID:             rev.ID.String(),
			RevisionNumber: rev.RevisionNumber,
			AuthorID:       rev.AuthorID.String(),
			Message:        textOrEmpty(rev.Message),
			CreatedAt:      rev.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": views})
}

func (s *Server) handleForkRecipe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}

	src, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	// private recipes cannot be forked by others — 404 to avoid leaking existence
	if src.Visibility == queries.VisibilityPrivate && src.AuthorID != user.ID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	var req forkRecipeRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	name := src.Name
	if req.Name != nil && *req.Name != "" {
		if len(*req.Name) > maxRecipeName {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name too long (max 200 chars)"})
			return
		}
		name = *req.Name
	}

	srcIngs, err := s.queries.ListRecipeIngredients(r.Context(), src.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}

	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}
	defer tx.Rollback(r.Context())
	qtx := s.queries.WithTx(tx)

	if err := qtx.IncrementForkCount(r.Context(), src.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}

	fork, err := qtx.CreateForkedRecipe(r.Context(), queries.CreateForkedRecipeParams{
		AuthorID:         user.ID,
		ParentID:         uuid.NullUUID{UUID: src.ID, Valid: true},
		ParentRevisionID: src.CurrentRevisionID,
		BrewType:         src.BrewType,
		Name:             name,
		Style:            src.Style,
		Description:      src.Description,
		TargetOg:         src.TargetOg,
		TargetFg:         src.TargetFg,
		TargetAbv:        src.TargetAbv,
		BatchSizeL:       src.BatchSizeL,
		Visibility:       queries.VisibilityPrivate,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}

	ingInputs := make([]ingredientInput, 0, len(srcIngs))
	for _, ing := range srcIngs {
		ingInputs = append(ingInputs, ingredientInput{
			Kind:      string(ing.Kind),
			Name:      ing.Name,
			Amount:    numericPtr(ing.Amount),
			Unit:      textOrEmpty(ing.Unit),
			SortOrder: ing.SortOrder,
			Details:   json.RawMessage(ing.Details),
		})
	}
	ingRows, err := s.insertIngredients(r.Context(), qtx, fork.ID, ingInputs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}

	var msgParam pgtype.Text
	if req.Message != nil {
		msgParam = pgtype.Text{String: *req.Message, Valid: true}
	}

	rev, err := qtx.CreateRecipeRevision(r.Context(), queries.CreateRecipeRevisionParams{
		RecipeID:       fork.ID,
		RevisionNumber: 1,
		AuthorID:       user.ID,
		Message:        msgParam,
		Name:           fork.Name,
		Style:          fork.Style,
		Description:    fork.Description,
		TargetOg:       fork.TargetOg,
		TargetFg:       fork.TargetFg,
		TargetAbv:      fork.TargetAbv,
		BatchSizeL:     fork.BatchSizeL,
		Ingredients:    buildSnapshot(ingRows),
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}

	fork, err = qtx.SetRecipeRevision(r.Context(), queries.SetRecipeRevisionParams{
		ID:                fork.ID,
		CurrentRevisionID: uuid.NullUUID{UUID: rev.ID, Valid: true},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fork failed"})
		return
	}
	writeJSON(w, http.StatusCreated, toRecipeView(fork, rev, ingRows))
}

func (s *Server) handleGetRevision(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	revNum, err := strconv.ParseInt(chi.URLParam(r, "rev"), 10, 32)
	if err != nil || revNum < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid revision number"})
		return
	}

	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	viewer, _ := userFromContext(r.Context())
	if !recipeVisibleTo(recipe, viewer) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}

	rev, err := s.queries.GetRecipeRevisionByNumber(r.Context(), queries.GetRecipeRevisionByNumberParams{
		RecipeID:       id,
		RevisionNumber: int32(revNum),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	writeJSON(w, http.StatusOK, toRevisionDetail(rev))
}

func (s *Server) handleListComments(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	viewer, _ := userFromContext(r.Context())
	if !recipeVisibleTo(recipe, viewer) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	p, ok := parseListPagination(w, r)
	if !ok {
		return
	}
	rows, err := s.queries.ListRecipeComments(r.Context(), queries.ListRecipeCommentsParams{
		RecipeID: id,
		CursorTs: p.CursorTs,
		CursorID: p.CursorID,
		LimitN:   p.Limit,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	views := make([]commentView, 0, len(rows))
	for _, row := range rows {
		views = append(views, commentView{
			ID:             row.ID.String(),
			RecipeID:       row.RecipeID.String(),
			AuthorID:       row.AuthorID.String(),
			AuthorUsername: row.AuthorUsername,
			Body:           row.Body,
			CreatedAt:      row.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
		})
	}
	next := nextCursor(len(rows), p.Limit, func() (time.Time, uuid.UUID) {
		last := rows[len(rows)-1]
		return last.CreatedAt.Time, last.ID
	})
	writeJSON(w, http.StatusOK, map[string]any{"comments": views, "next_cursor": nullableCursor(next)})
}

func (s *Server) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	if !recipeVisibleTo(recipe, user) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	var req createCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Body == "" || len(req.Body) > maxCommentBody {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body required (max 10 KiB)"})
		return
	}
	comment, err := s.queries.CreateRecipeComment(r.Context(), queries.CreateRecipeCommentParams{
		RecipeID: id,
		AuthorID: user.ID,
		Body:     req.Body,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, commentView{
		ID:             comment.ID.String(),
		RecipeID:       comment.RecipeID.String(),
		AuthorID:       comment.AuthorID.String(),
		AuthorUsername: user.Username,
		Body:           comment.Body,
		CreatedAt:      comment.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
	})
}

func (s *Server) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	commentID, err := parseUUIDParam(r, "commentId")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid comment id"})
		return
	}
	rows, err := s.queries.DeleteRecipeComment(r.Context(), queries.DeleteRecipeCommentParams{
		ID:       commentID,
		AuthorID: user.ID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}
	if rows == 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLikeRecipe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	recipe, err := s.queries.GetRecipeByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "fetch failed"})
		return
	}
	if !recipeVisibleTo(recipe, user) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err := s.queries.LikeRecipe(r.Context(), queries.LikeRecipeParams{
		UserID:   user.ID,
		RecipeID: id,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "like failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleUnlikeRecipe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	if err := s.queries.UnlikeRecipe(r.Context(), queries.UnlikeRecipeParams{
		UserID:   user.ID,
		RecipeID: id,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unlike failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
