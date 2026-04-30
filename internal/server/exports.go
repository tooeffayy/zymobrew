package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"zymobrew/internal/jobs"
	"zymobrew/internal/queries"
)

// requireAdmin is a middleware that returns 403 for non-admin users.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := userFromContext(r.Context())
		if !ok || !user.IsAdmin {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- User exports ---

func (s *Server) handleTriggerExport(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	// Empty body is the common case — JS clients posting JSON, curl with no
	// `-d`, etc. We treat it as "default zip" rather than 400ing.
	var req struct {
		Format string `json:"format"`
	}
	if r.ContentLength != 0 && r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
	}
	if req.Format == "" {
		req.Format = jobs.ExportFormatZip
	}
	if !jobs.IsValidExportFormat(req.Format) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "format must be one of: zip, tar.gz, zstd"})
		return
	}

	// Reject if there is already an active export for this user.
	_, err := s.queries.GetPendingUserExport(r.Context(), user.ID)
	if err == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an export is already in progress"})
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	export, err := s.queries.CreateUserExport(r.Context(), queries.CreateUserExportParams{
		UserID: user.ID,
		Format: req.Format,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusAccepted, exportView(export))
}

func (s *Server) handleListExports(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	exports, err := s.queries.ListUserExports(r.Context(), user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	out := make([]map[string]any, 0, len(exports))
	for _, e := range exports {
		out = append(out, exportView(e))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetExport(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	export, err := s.queries.GetUserExport(r.Context(), queries.GetUserExportParams{ID: id, UserID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, exportView(export))
}

// handleDownloadExport streams the user's export archive directly from the
// local export store. After a successful stream, the row is flipped to
// 'expired' and the file is deleted — exports are single-use, with the
// per-row TTL acting as a safety net for the never-downloaded case.
//
// The DB flip happens before the disk delete so a crash between the two only
// orphans bytes on disk (harmless), never leaves a row pointing at a missing
// file. ExpireUserExportByID's WHERE clause requires status='complete', so
// concurrent downloaders all stream successfully but only one wins the flip
// and performs the delete.
func (s *Server) handleDownloadExport(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	export, err := s.queries.GetUserExport(r.Context(), queries.GetUserExportParams{ID: id, UserID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if export.Status != queries.JobStatusComplete {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("export status is %s", export.Status)})
		return
	}

	rc, size, err := s.exportStore.Get(r.Context(), export.FilePath.String)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	defer rc.Close()

	ext := jobs.FormatExtension(export.Format)
	if sha := textOrEmpty(export.Sha256); sha != "" {
		w.Header().Set("X-Content-SHA256", sha)
	}
	w.Header().Set("Content-Type", jobs.FormatContentType(export.Format))
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="zymo-export-%s.%s"`, id, ext))
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, rc); err != nil {
		// io.Copy error → we know the client didn't receive everything; leave
		// the row alone so they can retry. The reverse (nil error → client
		// got the bytes) isn't strictly true under kernel-side write
		// buffering on small archives, so the post-stream cleanup below is
		// best-effort. The per-row TTL is the backstop for that case.
		return
	}

	// Successful stream — race-safely retire the export. Use a fresh ctx so
	// a client closing the connection right after the last byte doesn't
	// cancel the cleanup writes.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deletedPath, err := s.queries.ExpireUserExportByID(cleanupCtx, queries.ExpireUserExportByIDParams{ID: id, UserID: user.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the race — another concurrent download already retired this export.
		return
	}
	if err != nil {
		slog.Error("expire user export after download", "export_id", id, "err", err)
		return
	}
	if deletedPath.Valid && deletedPath.String != "" {
		if err := s.exportStore.Delete(cleanupCtx, deletedPath.String); err != nil {
			slog.Error("delete user export blob after download", "export_id", id, "path", deletedPath.String, "err", err)
		}
	}
}

// --- Admin backups ---

func (s *Server) handleTriggerAdminBackup(w http.ResponseWriter, r *http.Request) {
	backup, err := s.queries.CreateAdminBackup(r.Context(), s.backupStore.Backend())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusAccepted, backupView(backup))
}

func (s *Server) handleListAdminBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := s.queries.ListAdminBackups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	out := make([]map[string]any, 0, len(backups))
	for _, b := range backups {
		out = append(out, backupView(b))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetAdminBackup(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	backup, err := s.queries.GetAdminBackup(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, backupView(backup))
}

func (s *Server) handleDownloadAdminBackup(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return
	}
	backup, err := s.queries.GetAdminBackup(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if backup.Status != queries.JobStatusComplete {
		writeJSON(w, http.StatusConflict, map[string]string{"error": fmt.Sprintf("backup status is %s", backup.Status)})
		return
	}
	s.serveStorageFile(w, r, backup.FilePath.String, fmt.Sprintf("zymo-backup-%s.dump", id), "application/octet-stream", textOrEmpty(backup.Sha256))
}

// serveStorageFile is the admin-backup download path. It either redirects
// to a presigned URL (S3 backend) or streams the file directly (local
// backend). When sha256 is non-empty it's surfaced as X-Content-SHA256 on
// the response, useful for `curl | sha256sum -c -` integrity checks. On the
// S3 redirect path the header rides on the 302; the actual S3 response
// won't carry it, so clients verifying after a redirect should rely on the
// JSON view's `sha256` field instead.
//
// User-export downloads do NOT route through here — they always stream from
// the local-only export store and clean up after themselves. See
// handleDownloadExport.
func (s *Server) serveStorageFile(w http.ResponseWriter, r *http.Request, key, filename, contentType, sha256 string) {
	if sha256 != "" {
		w.Header().Set("X-Content-SHA256", sha256)
	}
	presigned, err := s.backupStore.PresignGet(r.Context(), key, 15*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	if presigned != "" {
		http.Redirect(w, r, presigned, http.StatusFound)
		return
	}
	rc, size, err := s.backupStore.Get(r.Context(), key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	defer rc.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Type", contentType)
	if size > 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// --- View helpers ---

func exportView(e queries.UserExport) map[string]any {
	v := map[string]any{
		"id":         e.ID,
		"status":     e.Status,
		"format":     e.Format,
		"created_at": e.CreatedAt.Time,
	}
	if e.SizeBytes.Valid {
		v["size_bytes"] = e.SizeBytes.Int64
	}
	if e.Sha256.Valid {
		v["sha256"] = e.Sha256.String
	}
	if e.CompletedAt.Valid {
		v["completed_at"] = e.CompletedAt.Time
	}
	if e.ExpiresAt.Valid {
		v["expires_at"] = e.ExpiresAt.Time
	}
	if e.Error.Valid {
		v["error"] = e.Error.String
	}
	return v
}

func backupView(b queries.AdminBackup) map[string]any {
	v := map[string]any{
		"id":              b.ID,
		"status":          b.Status,
		"storage_backend": b.StorageBackend,
		"created_at":      b.CreatedAt.Time,
	}
	if b.SizeBytes.Valid {
		v["size_bytes"] = b.SizeBytes.Int64
	}
	if b.Sha256.Valid {
		v["sha256"] = b.Sha256.String
	}
	if b.CompletedAt.Valid {
		v["completed_at"] = b.CompletedAt.Time
	}
	if b.Error.Valid {
		v["error"] = b.Error.String
	}
	return v
}
