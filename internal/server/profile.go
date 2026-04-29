package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/account"
	"zymobrew/internal/auth"
	"zymobrew/internal/queries"
)

const (
	maxDisplayNameLen = 64
	maxBioBytes       = 2 * 1024
	maxAvatarURLBytes = 512
)

// publicProfileView is the shape returned for any user visible to the public.
// It intentionally omits email.
type publicProfileView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	Bio         string `json:"bio,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	CreatedAt   string `json:"created_at"`
}

func toPublicProfile(u queries.User) publicProfileView {
	v := publicProfileView{
		ID:        u.ID.String(),
		Username:  u.Username,
		CreatedAt: u.CreatedAt.Time.Format("2006-01-02T15:04:05Z"),
	}
	if u.DisplayName.Valid {
		v.DisplayName = u.DisplayName.String
	}
	if u.Bio.Valid {
		v.Bio = u.Bio.String
	}
	if u.AvatarUrl.Valid {
		v.AvatarURL = u.AvatarUrl.String
	}
	return v
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	user, err := s.queries.GetUserByUsername(r.Context(), username)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}
	writeJSON(w, http.StatusOK, toPublicProfile(user))
}

type updateProfileRequest struct {
	DisplayName *string `json:"display_name"`
	Bio         *string `json:"bio"`
	AvatarURL   *string `json:"avatar_url"`
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.DisplayName != nil && len(*req.DisplayName) > maxDisplayNameLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "display_name too long"})
		return
	}
	if req.Bio != nil && len(*req.Bio) > maxBioBytes {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bio too long"})
		return
	}
	if req.AvatarURL != nil {
		trimmed := strings.TrimSpace(*req.AvatarURL)
		if len(trimmed) > maxAvatarURLBytes {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "avatar_url too long"})
			return
		}
	}

	params := queries.UpdateUserParams{ID: user.ID}
	if req.DisplayName != nil {
		params.DisplayName = pgtype.Text{String: *req.DisplayName, Valid: true}
	}
	if req.Bio != nil {
		params.Bio = pgtype.Text{String: *req.Bio, Valid: true}
	}
	if req.AvatarURL != nil {
		params.AvatarUrl = pgtype.Text{String: strings.TrimSpace(*req.AvatarURL), Valid: true}
	}

	updated, err := s.queries.UpdateUser(r.Context(), params)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}
	writeJSON(w, http.StatusOK, toPublicProfile(updated))
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if len(req.NewPassword) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password too short"})
		return
	}

	// Reject accounts that have no password (e.g. future OIDC-only users).
	if !user.PasswordHash.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account has no password set"})
		return
	}
	if err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash.String); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password incorrect"})
		return
	}
	if req.NewPassword == req.CurrentPassword {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new password must differ from current"})
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash failed"})
		return
	}
	if err := s.queries.UpdateUserPassword(r.Context(), queries.UpdateUserPasswordParams{
		ID:           user.ID,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
		return
	}

	// Rotate all sessions: kick other devices, issue a fresh token to the caller.
	// If the delete fails we must not silently issue new credentials — old
	// sessions would stay valid and a compromised token would survive the
	// password change.
	if err := s.queries.DeleteSessionsForUser(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session rotate failed"})
		return
	}
	token, err := s.issueSession(r.Context(), w, r, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session rotate failed"})
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, User: toPublicUser(*user)})
}

type deleteAccountRequest struct {
	Password string `json:"password"`
}

// handleDeleteAccount anonymizes the caller's account in place: PII is
// stripped from the user row, identifying tables (sessions, push devices,
// notification prefs/notifications, exports) are wiped, but recipes,
// comments, batches, and audit history are preserved attached to the
// anonymized row. An account_deletion_requests row is recorded so a
// post-restore reprocess pass can re-anonymize if a backup undoes the
// change.
func (s *Server) handleDeleteAccount(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())

	if user.IsAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin must hand off role before deletion"})
		return
	}

	var req deleteAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if !user.PasswordHash.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account has no password set"})
		return
	}
	if err := auth.VerifyPassword(req.Password, user.PasswordHash.String); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "password incorrect"})
		return
	}

	// Record the request first so a backup-restore reprocess can find it
	// even if anonymization itself partially fails. The CASCADE FK on user_id
	// keeps it tied to the (still-present, soon-anonymized) user row.
	if _, err := s.queries.CreateAccountDeletionRequest(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}

	if err := account.Anonymize(r.Context(), s.pool, s.queries, s.store, user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}

	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
