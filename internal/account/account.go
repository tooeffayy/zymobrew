// Package account contains the anonymization routine that the
// DELETE /api/users/me handler and the `zymo reprocess-deletions` command
// both run. It strips PII from a user row in place and clears identifying
// satellite tables, leaving public content (recipes, comments) attached
// to the now-anonymized user.
package account

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"zymobrew/internal/queries"
	"zymobrew/internal/storage"
)

// Anonymize runs the deletion sequence for a single user inside a single
// transaction, then best-effort cleans up export blobs from storage. Safe
// to call against an already-anonymized user — `AnonymizeUser` is
// idempotent and the satellite deletes are no-ops on empty rows.
func Anonymize(ctx context.Context, pool *pgxpool.Pool, q *queries.Queries, store storage.Store, userID uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := q.WithTx(tx)

	// Capture file paths before deleting export rows so we can clean blobs
	// after the tx commits.
	exportPaths, err := qtx.ListUserExportFilePathsForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list export paths: %w", err)
	}

	if err := qtx.DeleteSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete sessions: %w", err)
	}
	if err := qtx.DeletePushDevicesForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete push devices: %w", err)
	}
	if err := qtx.DeleteNotificationsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete notifications: %w", err)
	}
	if err := qtx.DeleteNotificationPrefsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete notification prefs: %w", err)
	}
	if err := qtx.DeleteUserExportsForUser(ctx, userID); err != nil {
		return fmt.Errorf("delete user exports: %w", err)
	}
	if err := qtx.AnonymizeUser(ctx, userID); err != nil {
		return fmt.Errorf("anonymize user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	for _, p := range exportPaths {
		if !p.Valid || p.String == "" {
			continue
		}
		if err := store.Delete(ctx, p.String); err != nil {
			slog.Error("delete export file on anonymize", "path", p.String, "user_id", userID, "err", err)
		}
	}
	return nil
}
