package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/auth"
	"zymobrew/internal/config"
	"zymobrew/internal/queries"
)

// pgUniqueViolation is the SQLSTATE code Postgres returns for a violated
// UNIQUE constraint. Used to distinguish "username/email taken" from real
// database errors so the latter surface as 500s.
const pgUniqueViolation = "23505"

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

const (
	sessionCookieName = "zymo_session"
	sessionDuration   = 30 * 24 * time.Hour
	minPasswordLen    = 8
)

var usernameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,32}$`)

type userKey struct{}

func userFromContext(ctx context.Context) (*queries.User, bool) {
	u, ok := ctx.Value(userKey{}).(*queries.User)
	return u, ok
}

// authMiddleware loads the user from the session cookie / bearer token onto
// the request context. Anonymous requests pass through; route-level guarding
// is done by requireAuth.
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := extractToken(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		row, err := s.queries.GetSessionWithUser(r.Context(), auth.HashSessionToken(token))
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userKey{}, &row.User)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := userFromContext(r.Context()); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func extractToken(r *http.Request) string {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		return cookie.Value
	}
	// RFC 7235: the auth scheme name is case-insensitive.
	if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// --- handlers --------------------------------------------------------------

type registerRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type authResponse struct {
	Token string         `json:"token"`
	User  publicUserView `json:"user"`
}

type publicUserView struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
}

func toPublicUser(u queries.User) publicUserView {
	v := publicUserView{
		ID:       u.ID.String(),
		Username: u.Username,
		Email:    u.Email,
	}
	if u.DisplayName.Valid {
		v.DisplayName = u.DisplayName.String
	}
	return v
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if !usernameRE.MatchString(req.Username) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username must be 3-32 chars [a-zA-Z0-9_-]"})
		return
	}
	if !strings.Contains(req.Email, "@") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
		return
	}
	if len(req.Password) < minPasswordLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password too short"})
		return
	}
	if err := s.checkRegistrationAllowed(r.Context()); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash failed"})
		return
	}
	display := pgtype.Text{}
	if req.DisplayName != "" {
		display = pgtype.Text{String: req.DisplayName, Valid: true}
	}
	user, err := s.queries.CreateUserWithPassword(r.Context(), queries.CreateUserWithPasswordParams{
		Username:     req.Username,
		Email:        req.Email,
		DisplayName:  display,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
	})
	if err != nil {
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "username or email already taken"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user create failed"})
		return
	}

	token, err := s.issueSession(r.Context(), w, r, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session create failed"})
		return
	}
	writeJSON(w, http.StatusCreated, authResponse{Token: token, User: toPublicUser(user)})
}

// checkRegistrationAllowed enforces INSTANCE_MODE policy:
//   - open:        always allowed
//   - single_user: allowed only while users is empty (bootstraps the admin)
//   - closed:      never via this endpoint (CLI-only user creation)
func (s *Server) checkRegistrationAllowed(ctx context.Context) error {
	switch s.cfg.InstanceMode {
	case config.ModeOpen:
		return nil
	case config.ModeClosed:
		return errors.New("registration disabled")
	case config.ModeSingleUser:
		count, err := s.queries.CountUsers(ctx)
		if err != nil {
			return errors.New("failed to check user count")
		}
		if count > 0 {
			return errors.New("registration disabled (single-user mode)")
		}
		return nil
	}
	return errors.New("unknown instance mode")
}

type loginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	id, hash, ok := s.lookupCredential(r.Context(), req.Identifier)
	// Always run argon2 against either the real hash or a dummy, so the
	// response time does not leak whether the user exists.
	verifyAgainst := auth.DummyHash()
	if ok && hash.Valid {
		verifyAgainst = hash.String
	}
	verifyErr := auth.VerifyPassword(req.Password, verifyAgainst)
	if !ok || !hash.Valid || verifyErr != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	user, err := s.queries.GetUserByID(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "user fetch failed"})
		return
	}
	token, err := s.issueSession(r.Context(), w, r, user.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session create failed"})
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, User: toPublicUser(user)})
}

func (s *Server) lookupCredential(ctx context.Context, identifier string) (uuid.UUID, pgtype.Text, bool) {
	if strings.Contains(identifier, "@") {
		row, err := s.queries.GetUserCredentialByEmail(ctx, identifier)
		if err != nil {
			return uuid.Nil, pgtype.Text{}, false
		}
		return row.ID, row.PasswordHash, true
	}
	row, err := s.queries.GetUserCredentialByUsername(ctx, identifier)
	if err != nil {
		return uuid.Nil, pgtype.Text{}, false
	}
	return row.ID, row.PasswordHash, true
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := extractToken(r); token != "" {
		_ = s.queries.DeleteSessionByTokenHash(r.Context(), auth.HashSessionToken(token))
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, _ := userFromContext(r.Context())
	writeJSON(w, http.StatusOK, toPublicUser(*user))
}

// --- session helpers -------------------------------------------------------

func (s *Server) issueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID uuid.UUID) (string, error) {
	raw, hash, err := auth.NewSessionToken()
	if err != nil {
		return "", err
	}
	expires := time.Now().Add(sessionDuration)
	ua := pgtype.Text{}
	if v := r.UserAgent(); v != "" {
		ua = pgtype.Text{String: v, Valid: true}
	}
	if _, err := s.queries.CreateSession(ctx, queries.CreateSessionParams{
		UserID:    userID,
		TokenHash: hash,
		UserAgent: ua,
		Ip:        nil,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return "", err
	}
	s.setSessionCookie(w, raw, expires)
	return raw, nil
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
	})
}
