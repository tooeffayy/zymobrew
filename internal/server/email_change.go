package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/auth"
	"zymobrew/internal/queries"
)

// emailChangeTTL is how long a pending email change (and its confirm/cancel
// links) stays valid before the dispatcher GC sweeps it.
const emailChangeTTL = 24 * time.Hour

// baseURL returns the instance's public origin for building absolute links in
// outbound mail. Prefers the explicit BASE_URL config (trusted; set this
// behind a proxy). Falls back to the request's Host with the scheme inferred
// from COOKIE_SECURE — convenient for local/dev, but a spoofable Host header
// is why production should set BASE_URL.
func (s *Server) baseURL(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return s.cfg.BaseURL
	}
	scheme := "http"
	if s.cfg.CookieSecure {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

type changeEmailRequest struct {
	CurrentPassword string `json:"current_password"`
	Email           string `json:"email"`
}

// handleChangeEmail initiates an email change. It does NOT mutate users.email:
// it re-authenticates with the current password, then records a pending
// change and mails (1) a confirmation link to the NEW address — the change
// only lands when that link is clicked, proving the address is real and
// controlled by the requester — and (2) a notice with a cancel link to the
// OLD address, so the legitimate owner can abort a change they didn't start.
//
// Requires SMTP to be configured (the whole flow is email-driven): 503 if not.
func (s *Server) handleChangeEmail(w http.ResponseWriter, r *http.Request) {
	if s.cfg.SMTPHost == "" || s.cfg.SMTPFrom == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "smtp not configured on this instance"})
		return
	}
	user, _ := userFromContext(r.Context())

	var req changeEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	newEmail := strings.TrimSpace(req.Email)
	// Parse as a bare addr-spec — rejects header-injection payloads and
	// display-name forms ("Foo <a@b>") before we ever store or send to it.
	if addr, err := mail.ParseAddress(newEmail); err != nil || addr.Address != newEmail {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if strings.EqualFold(newEmail, user.Email) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "new email matches current email"})
		return
	}

	// Cap initiations per user: the endpoint mails a caller-supplied address,
	// so an unbounded one is a spam amplifier; this also bounds online
	// password guessing through the re-auth below. Checked before the
	// password verify so a wrong-password attempt still consumes a token.
	if !s.emailChange.Allow(user.ID.String()) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many email-change requests"})
		return
	}

	// Re-authenticate: a valid session alone must not be enough to change the
	// account's recovery/identity anchor (closes the hijacked-session vector).
	if !user.PasswordHash.Valid {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account has no password set"})
		return
	}
	if err := auth.VerifyPassword(req.CurrentPassword, user.PasswordHash.String); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "current password incorrect"})
		return
	}

	// Fail fast on a collision (email is CITEXT-unique). Not authoritative —
	// confirm-time recheck still applies — but gives immediate feedback.
	switch _, err := s.queries.GetUserCredentialByEmail(r.Context(), newEmail); {
	case err == nil:
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email already taken"})
		return
	case !errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "email change failed"})
		return
	}

	confirmRaw, confirmHash, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "email change failed"})
		return
	}
	cancelRaw, cancelHash, err := auth.NewSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "email change failed"})
		return
	}

	// One active change at a time — drop any prior pending request first.
	if err := s.queries.DeleteEmailChangeRequestsForUser(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "email change failed"})
		return
	}
	reqRow, err := s.queries.CreateEmailChangeRequest(r.Context(), queries.CreateEmailChangeRequestParams{
		UserID:           user.ID,
		NewEmail:         newEmail,
		OldEmail:         user.Email,
		ConfirmTokenHash: confirmHash,
		CancelTokenHash:  cancelHash,
		ExpiresAt:        pgtype.Timestamptz{Time: time.Now().Add(emailChangeTTL), Valid: true},
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "email change failed"})
		return
	}

	base := s.baseURL(r)
	confirmURL := base + "/email/confirm?token=" + confirmRaw
	cancelURL := base + "/email/cancel?token=" + cancelRaw

	// The confirmation mail to the NEW address is load-bearing — if it can't
	// be sent, there's no way to complete the change, so roll the request back
	// and report a relay error rather than leaving a dead pending row.
	confirmBody := fmt.Sprintf(
		"You (or someone using your Zymo account) asked to change this account's email to %s.\n\n"+
			"Confirm the change by opening this link:\n%s\n\n"+
			"The link expires in 24 hours. If you didn't request this, you can ignore this email.\n",
		newEmail, confirmURL)
	if err := s.sendMail(r.Context(), s.cfg, newEmail, "Confirm your new Zymo email", confirmBody); err != nil {
		if delErr := s.queries.DeleteEmailChangeRequest(r.Context(), reqRow.ID); delErr != nil {
			slog.Error("email change: rollback after send failure", "err", delErr)
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}

	// The heads-up to the OLD address is the tripwire — best-effort, so a
	// failure here doesn't block the change the user legitimately requested.
	noticeBody := fmt.Sprintf(
		"A request was made to change your Zymo account email to %s.\n\n"+
			"The change won't take effect until it's confirmed from the new address. "+
			"If you didn't request this, cancel it immediately:\n%s\n\n"+
			"This link expires in 24 hours.\n",
		newEmail, cancelURL)
	if err := s.sendMail(r.Context(), s.cfg, user.Email, "Your Zymo email is being changed", noticeBody); err != nil {
		slog.Error("email change: old-address notice failed", "err", err)
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending", "email": newEmail})
}

type emailChangeTokenRequest struct {
	Token string `json:"token"`
}

// handleConfirmEmailChange completes a pending change when the confirmation
// link from the NEW address is followed. Public + token-gated: the person
// clicking may not be logged in (different device/browser), so the secret
// token is the sole capability. Re-checks uniqueness because another account
// could have claimed the address since the request was created.
func (s *Server) handleConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var req emailChangeTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token"})
		return
	}

	reqRow, err := s.queries.GetEmailChangeRequestByConfirmHash(r.Context(), auth.HashSessionToken(req.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired link"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "confirm failed"})
		return
	}

	updated, err := s.queries.UpdateUserEmail(r.Context(), queries.UpdateUserEmailParams{
		Email: reqRow.NewEmail,
		ID:    reqRow.UserID,
	})
	switch {
	case isUniqueViolation(err):
		// Address was taken between request and confirm. Leave the row so the
		// user could retry if it frees up before expiry.
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email already taken"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "confirm failed"})
		return
	}

	if err := s.queries.DeleteEmailChangeRequest(r.Context(), reqRow.ID); err != nil {
		// Email already switched; a lingering row only expires harmlessly.
		slog.Error("email change: cleanup after confirm", "err", err)
	}
	writeJSON(w, http.StatusOK, toPublicUser(updated))
}

// handleCancelEmailChange aborts a pending change via the cancel link mailed
// to the OLD address. Public + token-gated for the same reason as confirm —
// the legitimate owner must be able to stop a change without logging in.
func (s *Server) handleCancelEmailChange(w http.ResponseWriter, r *http.Request) {
	var req emailChangeTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if req.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token"})
		return
	}

	reqRow, err := s.queries.GetEmailChangeRequestByCancelHash(r.Context(), auth.HashSessionToken(req.Token))
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired link"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cancel failed"})
		return
	}
	if err := s.queries.DeleteEmailChangeRequest(r.Context(), reqRow.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "cancel failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
