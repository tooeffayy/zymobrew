package jobs

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"zymobrew/internal/queries"
	"zymobrew/internal/storage"
)

// UserExportDispatchArgs is the periodic dispatcher that claims pending export
// rows and processes each one inline. Runs every minute, same pattern as the
// reminder dispatcher.
type UserExportDispatchArgs struct{}

func (UserExportDispatchArgs) Kind() string { return "user_export_dispatcher" }

type userExportDispatchWorker struct {
	river.WorkerDefaults[UserExportDispatchArgs]
	queries *queries.Queries
	store   storage.Store
}

func (w *userExportDispatchWorker) Work(ctx context.Context, _ *river.Job[UserExportDispatchArgs]) error {
	pending, err := w.queries.ClaimPendingUserExports(ctx)
	if err != nil {
		return fmt.Errorf("claim pending exports: %w", err)
	}
	for _, row := range pending {
		if err := w.processExport(ctx, row); err != nil {
			slog.Error("user export failed", "export_id", row.ID, "err", err)
			_ = w.queries.FailUserExport(ctx, queries.FailUserExportParams{
				ID:    row.ID,
				Error: pgtype.Text{String: fmt.Sprintf("%v", err), Valid: true},
			})
		}
	}
	// Expire old completed exports on the same tick and remove their files.
	w.pruneExpiredExports(ctx)
	return nil
}

func (w *userExportDispatchWorker) processExport(ctx context.Context, row queries.UserExport) error {
	userID := row.UserID

	// Build the ZIP into a temp file so we know the size before uploading.
	tmp, err := os.CreateTemp("", "zymo-export-*.zip")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	if err := w.buildZIP(ctx, tmp, userID); err != nil {
		return fmt.Errorf("build zip: %w", err)
	}

	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	key := fmt.Sprintf("exports/users/%s/%s.zip", userID, row.ID)
	if err := w.store.Put(ctx, key, tmp, size); err != nil {
		return fmt.Errorf("store put: %w", err)
	}

	_, err = w.queries.CompleteUserExport(ctx, queries.CompleteUserExportParams{
		ID:        row.ID,
		FilePath:  pgtype.Text{String: key, Valid: true},
		SizeBytes: pgtype.Int8{Int64: size, Valid: true},
	})
	return err
}

func (w *userExportDispatchWorker) buildZIP(ctx context.Context, out io.Writer, userID uuid.UUID) error {
	zw := zip.NewWriter(out)
	defer zw.Close()

	user, err := w.queries.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	writeEntry := func(name string, v any) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		return json.NewEncoder(f).Encode(v)
	}

	// manifest.json
	if err := writeEntry("manifest.json", map[string]any{
		"schema_version": 1,
		"exported_at":    time.Now().UTC().Format(time.RFC3339),
		"username":       user.Username,
	}); err != nil {
		return err
	}

	// profile.json
	if err := writeEntry("profile.json", map[string]any{
		"id":           user.ID,
		"username":     user.Username,
		"display_name": user.DisplayName.String,
		"bio":          user.Bio.String,
		"avatar_url":   user.AvatarUrl.String,
		"created_at":   user.CreatedAt.Time,
	}); err != nil {
		return err
	}

	// recipes.json
	if err := w.writeRecipes(ctx, zw, userID); err != nil {
		return err
	}

	// batches.json
	if err := w.writeBatches(ctx, zw, userID); err != nil {
		return err
	}

	// social.json
	if err := w.writeSocial(ctx, zw, userID); err != nil {
		return err
	}

	return nil
}

func (w *userExportDispatchWorker) pruneExpiredExports(ctx context.Context) {
	filePaths, err := w.queries.ExpireUserExports(ctx)
	if err != nil {
		slog.Error("expire user exports", "err", err)
		return
	}
	for _, p := range filePaths {
		if !p.Valid || p.String == "" {
			continue
		}
		if err := w.store.Delete(ctx, p.String); err != nil {
			slog.Error("delete expired export file", "path", p.String, "err", err)
		}
	}
}

func (w *userExportDispatchWorker) writeRecipes(ctx context.Context, zw *zip.Writer, userID uuid.UUID) error {
	recipes, err := w.queries.ListAllRecipesForAuthor(ctx, userID)
	if err != nil {
		return err
	}
	type recipeExport struct {
		queries.Recipe
		Ingredients []queries.RecipeIngredient `json:"ingredients"`
		Revisions   []queries.RecipeRevision   `json:"revisions"`
	}
	out := make([]recipeExport, 0, len(recipes))
	for _, r := range recipes {
		ings, _ := w.queries.ListRecipeIngredients(ctx, r.ID)
		revs, _ := w.queries.ListRecipeRevisions(ctx, r.ID)
		out = append(out, recipeExport{Recipe: r, Ingredients: ings, Revisions: revs})
	}
	f, err := zw.Create("recipes.json")
	if err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(out)
}

func (w *userExportDispatchWorker) writeBatches(ctx context.Context, zw *zip.Writer, userID uuid.UUID) error {
	batches, err := w.queries.ListAllBatchesForUser(ctx, userID)
	if err != nil {
		return err
	}
	type batchExport struct {
		queries.Batch
		Readings     []queries.Reading   `json:"readings"`
		Events       []queries.BatchEvent `json:"events"`
		TastingNotes []queries.TastingNote `json:"tasting_notes"`
	}
	out := make([]batchExport, 0, len(batches))
	for _, b := range batches {
		readings, _ := w.queries.ListReadingsForBatch(ctx, b.ID)
		events, _ := w.queries.ListBatchEventsForBatch(ctx, b.ID)
		notes, _ := w.queries.ListTastingNotesForBatch(ctx, b.ID)
		out = append(out, batchExport{Batch: b, Readings: readings, Events: events, TastingNotes: notes})
	}
	f, err := zw.Create("batches.json")
	if err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(out)
}

func (w *userExportDispatchWorker) writeSocial(ctx context.Context, zw *zip.Writer, userID uuid.UUID) error {
	follows, _ := w.queries.ListFollowsByUser(ctx, userID)
	likes, _ := w.queries.ListLikesByUser(ctx, userID)
	comments, _ := w.queries.ListRecipeCommentsByUser(ctx, userID)

	f, err := zw.Create("social.json")
	if err != nil {
		return err
	}
	return json.NewEncoder(f).Encode(map[string]any{
		"follows":  follows,
		"likes":    likes,
		"comments": comments,
	})
}
