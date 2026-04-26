package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
	"zymobrew/internal/testutil"
)

// setupAuth returns a fresh server with the users/sessions tables emptied so
// mode gating (single-user "first registration only") tests are deterministic.
func setupAuth(t *testing.T, mode config.InstanceMode) (*server.Server, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	if _, err := pool.Exec(ctx, "TRUNCATE users, sessions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	return server.New(pool, config.Config{InstanceMode: mode}), pool
}

func doJSON(t *testing.T, srv *server.Server, method, path string, body any, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec.Result()
}

func decode(t *testing.T, r *http.Response, into any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

func TestAuth_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	// Register
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: got %d", resp.StatusCode)
	}
	regCookies := resp.Cookies()
	if len(regCookies) == 0 {
		t.Fatal("register: expected Set-Cookie")
	}
	var reg struct {
		Token string `json:"token"`
		User  struct {
			ID, Username, Email string
		} `json:"user"`
	}
	decode(t, resp, &reg)
	if reg.Token == "" || reg.User.Username != "alice" {
		t.Fatalf("register response: %+v", reg)
	}

	// /me with cookie
	resp = doJSON(t, srv, http.MethodGet, "/api/auth/me", nil, regCookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me: got %d", resp.StatusCode)
	}

	// Login with username
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice",
		"password":   "supersecret",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login by username: got %d", resp.StatusCode)
	}
	loginCookies := resp.Cookies()

	// Login with email
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice@example.com",
		"password":   "supersecret",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login by email: got %d", resp.StatusCode)
	}

	// Logout the second login's session
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/logout", nil, loginCookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: got %d", resp.StatusCode)
	}

	// /me with the now-invalidated cookie should be 401
	resp = doJSON(t, srv, http.MethodGet, "/api/auth/me", nil, loginCookies...)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/me after logout: got %d", resp.StatusCode)
	}
}

func TestAuth_Register_Validation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	cases := []struct {
		name string
		body map[string]string
	}{
		{"bad-username", map[string]string{"username": "ab", "email": "a@b.com", "password": "longenough"}},
		{"bad-email", map[string]string{"username": "validone", "email": "noatsign", "password": "longenough"}},
		{"short-password", map[string]string{"username": "validone", "email": "a@b.com", "password": "short"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", c.body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestAuth_Register_ClosedMode(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeClosed)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("got %d, want 403", resp.StatusCode)
	}
}

func TestAuth_Register_SingleUserMode(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeSingleUser)

	// First registration is the bootstrap admin — allowed.
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first register: got %d", resp.StatusCode)
	}

	// Second registration must be rejected.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "second",
		"email":    "second@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second register: got %d, want 403", resp.StatusCode)
	}
}

func TestAuth_Login_BadCredentials(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})

	cases := []struct {
		name string
		body map[string]string
	}{
		{"wrong-password", map[string]string{"identifier": "alice", "password": "nope1234"}},
		{"unknown-user", map[string]string{"identifier": "ghost", "password": "supersecret"}},
		{"unknown-email", map[string]string{"identifier": "ghost@example.com", "password": "supersecret"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPost, "/api/auth/login", c.body)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", resp.StatusCode)
			}
		})
	}
}

func TestAuth_BodySizeLimit(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	// 2 MiB blob — well over the 1 MiB cap.
	junk := bytes.Repeat([]byte("a"), 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(junk))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK || rec.Code == http.StatusUnauthorized {
		t.Fatalf("oversized body should be rejected, got %d", rec.Code)
	}
}

func TestAuth_Me_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodGet, "/api/auth/me", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestAuth_BearerToken(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: got %d", resp.StatusCode)
	}
	var reg struct {
		Token string `json:"token"`
	}
	decode(t, resp, &reg)
	if reg.Token == "" {
		t.Fatal("expected token in response body")
	}

	// Use the token via Authorization header (mobile / API client path)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+reg.Token)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}
