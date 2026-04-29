package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"zymobrew/internal/auth"
	"zymobrew/internal/config"
	"zymobrew/internal/queries"
)

// SQLSTATE codes we route on. Lets us distinguish user-input issues (4xx)
// from infrastructure problems (5xx), without letting raw Postgres error
// strings leak to clients.
const (
	pgUniqueViolation         = "23505"
	pgInvalidTextRepresentation = "22P02" // includes invalid enum values
	pgSerializationFailure    = "40001"
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation
}

func isInvalidTextRepresentation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgInvalidTextRepresentation
}

func isSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgSerializationFailure
}

var errRegistrationClosed = errors.New("registration closed")

const (
	sessionCookieName    = "zymo_session"
	sessionDuration      = 30 * 24 * time.Hour
	sessionTouchInterval = 5 * time.Minute
	minPasswordLen       = 8
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
		// Touch last_seen_at if it's stale. Throttling avoids one write per
		// request on busy sessions; the threshold uses the value already
		// loaded above, no extra SELECT. Fire-and-forget — a failed touch
		// shouldn't fail the request.
		if !row.Session.LastSeenAt.Valid || time.Since(row.Session.LastSeenAt.Time) > sessionTouchInterval {
			go func(id uuid.UUID) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = s.queries.TouchSession(ctx, id)
			}(row.Session.ID)
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
	switch s.cfg.InstanceMode {
	case config.ModeOpen, config.ModeSingleUser:
		// fall through to insert path
	case config.ModeClosed:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "registration disabled"})
		return
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unknown instance mode"})
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
	isAdmin := s.cfg.InstanceMode == config.ModeSingleUser
	params := queries.CreateUserWithPasswordParams{
		Username:     req.Username,
		Email:        req.Email,
		DisplayName:  display,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
		IsAdmin:      isAdmin,
	}

	user, err := s.createRegistrationUser(r.Context(), params)
	switch {
	case errors.Is(err, errRegistrationClosed):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "registration disabled (single-user mode)"})
		return
	case isUniqueViolation(err):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "username or email already taken"})
		return
	case err != nil:
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

// createRegistrationUser inserts the user, atomically gating single-user mode
// on an empty users table. SERIALIZABLE here closes a TOCTOU where two
// concurrent bootstraps both observe count=0 and both insert admin rows; the
// loser's commit aborts with 40001, which we surface as "registration closed"
// — the same outcome they'd see if they'd retried after the winner committed.
func (s *Server) createRegistrationUser(ctx context.Context, params queries.CreateUserWithPasswordParams) (queries.User, error) {
	if s.cfg.InstanceMode != config.ModeSingleUser {
		return s.queries.CreateUserWithPassword(ctx, params)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return queries.User{}, err
	}
	defer tx.Rollback(ctx)
	qtx := s.queries.WithTx(tx)
	count, err := qtx.CountUsers(ctx)
	if err != nil {
		return queries.User{}, err
	}
	if count > 0 {
		return queries.User{}, errRegistrationClosed
	}
	user, err := qtx.CreateUserWithPassword(ctx, params)
	if err != nil {
		return queries.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		if isSerializationFailure(err) {
			return queries.User{}, errRegistrationClosed
		}
		return queries.User{}, err
	}
	return user, nil
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

	// Per-identifier throttle. The IP middleware already gates raw flood;
	// this stops a single legitimate IP from brute-forcing one account.
	// Lowercased so "Alice" and "alice" share a bucket (CITEXT semantics).
	if req.Identifier != "" && !s.loginUser.Allow(strings.ToLower(req.Identifier)) {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many login attempts for this account"})
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
	var ip *netip.Addr
	if a := clientIPFromContext(r.Context()); a.IsValid() {
		ip = &a
	}
	if _, err := s.queries.CreateSession(ctx, queries.CreateSessionParams{
		UserID:    userID,
		TokenHash: hash,
		UserAgent: ua,
		Ip:        ip,
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
