package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"zymobrew/internal/config"
)

func TestProfile_GetPublic(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})

	resp := doJSON(t, srv, http.MethodGet, "/api/users/alice", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)

	if body["username"] != "alice" {
		t.Errorf("username: got %v", body["username"])
	}
	if _, hasEmail := body["email"]; hasEmail {
		t.Error("public profile must not include email")
	}
	if body["id"] == nil || body["created_at"] == nil {
		t.Errorf("missing id or created_at: %v", body)
	}
}

func TestProfile_GetPublic_NotFound(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodGet, "/api/users/ghost", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.StatusCode)
	}
}

func TestProfile_Update_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPatch, "/api/users/me", map[string]string{
		"display_name": "Alice B",
		"bio":          "I brew mead.",
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch profile: got %d", resp.StatusCode)
	}
	var body map[string]any
	decode(t, resp, &body)
	if body["display_name"] != "Alice B" {
		t.Errorf("display_name: got %v", body["display_name"])
	}
	if body["bio"] != "I brew mead." {
		t.Errorf("bio: got %v", body["bio"])
	}

	// Omitted field stays unchanged — send another PATCH without display_name.
	resp = doJSON(t, srv, http.MethodPatch, "/api/users/me", map[string]string{
		"bio": "Updated bio.",
	}, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second patch: got %d", resp.StatusCode)
	}
	decode(t, resp, &body)
	if body["display_name"] != "Alice B" {
		t.Errorf("display_name should be unchanged, got %v", body["display_name"])
	}
	if body["bio"] != "Updated bio." {
		t.Errorf("bio: got %v", body["bio"])
	}
}

func TestProfile_Update_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPatch, "/api/users/me", map[string]string{
		"bio": "no auth",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestProfile_Update_Validation(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	cases := []struct {
		name string
		body map[string]string
	}{
		{"display_name_too_long", map[string]string{"display_name": strings.Repeat("a", 65)}},
		{"bio_too_long", map[string]string{"bio": strings.Repeat("x", 2049)}},
		{"avatar_url_too_long", map[string]string{"avatar_url": "https://example.com/" + strings.Repeat("a", 500)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := doJSON(t, srv, http.MethodPatch, "/api/users/me", c.body, cookies...)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("got %d, want 400", resp.StatusCode)
			}
		})
	}
}

func TestProfile_ChangePassword_HappyPath(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	oldCookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/users/me/password", map[string]string{
		"current_password": "supersecret",
		"new_password":     "newpassword123",
	}, oldCookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change password: got %d", resp.StatusCode)
	}
	var body struct {
		Token string `json:"token"`
	}
	decode(t, resp, &body)
	if body.Token == "" {
		t.Fatal("expected new token in response")
	}
	newCookies := resp.Cookies()
	if len(newCookies) == 0 {
		t.Fatal("expected Set-Cookie with new session")
	}

	// Old session must be invalidated.
	resp = doJSON(t, srv, http.MethodGet, "/api/auth/me", nil, oldCookies...)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old cookie after password change: got %d, want 401", resp.StatusCode)
	}

	// New session must work.
	resp = doJSON(t, srv, http.MethodGet, "/api/auth/me", nil, newCookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("new cookie after password change: got %d, want 200", resp.StatusCode)
	}

	// New password must work for subsequent logins.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice",
		"password":   "newpassword123",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login with new password: got %d", resp.StatusCode)
	}

	// Old password must not work.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice",
		"password":   "supersecret",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login with old password: got %d, want 401", resp.StatusCode)
	}
}

func TestProfile_ChangePassword_InvalidatesAllSessions(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	session1 := resp.Cookies()

	// Open a second session via login.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice",
		"password":   "supersecret",
	})
	session2 := resp.Cookies()

	// Change password using session1.
	resp = doJSON(t, srv, http.MethodPost, "/api/users/me/password", map[string]string{
		"current_password": "supersecret",
		"new_password":     "newpassword123",
	}, session1...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("change password: got %d", resp.StatusCode)
	}

	// Both old sessions must be invalidated.
	for _, cookies := range [][]*http.Cookie{session1, session2} {
		resp = doJSON(t, srv, http.MethodGet, "/api/auth/me", nil, cookies...)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("old session still valid after password change: got %d, want 401", resp.StatusCode)
		}
	}
}

func TestProfile_ChangePassword_SamePassword(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/users/me/password", map[string]string{
		"current_password": "supersecret",
		"new_password":     "supersecret",
	}, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestProfile_ChangePassword_WrongCurrent(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/users/me/password", map[string]string{
		"current_password": "wrongpassword",
		"new_password":     "newpassword123",
	}, cookies...)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestProfile_ChangePassword_ShortNew(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	resp = doJSON(t, srv, http.MethodPost, "/api/users/me/password", map[string]string{
		"current_password": "supersecret",
		"new_password":     "short",
	}, cookies...)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", resp.StatusCode)
	}
}

func TestProfile_ChangePassword_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/password", map[string]string{
		"current_password": "supersecret",
		"new_password":     "newpassword123",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestProfile_DeleteAccount_HappyPath(t *testing.T) {
	srv, pool := setupAuth(t, config.ModeOpen)
	ctx := context.Background()

	// Register and capture the user id so we can inspect post-anonymize state.
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()
	var registered map[string]any
	decode(t, resp, &registered)
	user := registered["user"].(map[string]any)
	userID := user["id"].(string)

	// Open a second session — must be revoked by the delete.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice",
		"password":   "supersecret",
	})
	otherCookies := resp.Cookies()

	// Wrong password is rejected without anonymizing.
	resp = doJSON(t, srv, http.MethodDelete, "/api/users/me", map[string]string{
		"password": "wrong",
	}, cookies...)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d, want 401", resp.StatusCode)
	}

	// Right password anonymizes and returns 204.
	resp = doJSON(t, srv, http.MethodDelete, "/api/users/me", map[string]string{
		"password": "supersecret",
	}, cookies...)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete account: got %d, want 204", resp.StatusCode)
	}

	// User row anonymized in place.
	var (
		username, email string
		passwordHash    *string
		deletedAt       *string
	)
	if err := pool.QueryRow(ctx,
		`SELECT username, email, password_hash, deleted_at::text FROM users WHERE id = $1`,
		userID,
	).Scan(&username, &email, &passwordHash, &deletedAt); err != nil {
		t.Fatalf("user row should still exist (anonymized): %v", err)
	}
	if !strings.HasPrefix(username, "deleted-") {
		t.Errorf("username not anonymized: %q", username)
	}
	if !strings.HasSuffix(email, "@deleted.invalid") {
		t.Errorf("email not anonymized: %q", email)
	}
	if passwordHash != nil {
		t.Errorf("password_hash should be NULL, got %q", *passwordHash)
	}
	if deletedAt == nil {
		t.Error("deleted_at should be set")
	}

	// Both sessions revoked.
	for name, c := range map[string][]*http.Cookie{"primary": cookies, "other": otherCookies} {
		resp = doJSON(t, srv, http.MethodGet, "/api/auth/me", nil, c...)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s session still valid: got %d, want 401", name, resp.StatusCode)
		}
	}

	// account_deletion_requests row recorded.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM account_deletion_requests WHERE user_id = $1`, userID,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("account_deletion_requests rows for user: got %d, want 1", n)
	}

	// Profile lookup 404s — anonymized users vanish from the public surface.
	resp = doJSON(t, srv, http.MethodGet, "/api/users/alice", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("anonymized profile lookup: got %d, want 404", resp.StatusCode)
	}

	// Username freed: re-register with the same handle now succeeds.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice2@example.com",
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Errorf("re-register after anonymize: got %d", resp.StatusCode)
	}
}

func TestProfile_DeleteAccount_AdminBlocked(t *testing.T) {
	srv, pool := setupAuth(t, config.ModeOpen)
	ctx := context.Background()

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "admin",
		"email":    "admin@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()
	if _, err := pool.Exec(ctx, "UPDATE users SET is_admin = TRUE WHERE username = 'admin'"); err != nil {
		t.Fatal(err)
	}

	resp = doJSON(t, srv, http.MethodDelete, "/api/users/me", map[string]string{
		"password": "supersecret",
	}, cookies...)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin self-delete: got %d, want 403", resp.StatusCode)
	}
}

func TestProfile_DeleteAccount_RequiresAuth(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodDelete, "/api/users/me", map[string]string{
		"password": "supersecret",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", resp.StatusCode)
	}
}

func TestProfile_GetPublic_ReflectsUpdate(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "supersecret",
	})
	cookies := resp.Cookies()

	doJSON(t, srv, http.MethodPatch, "/api/users/me", map[string]string{
		"bio": "Mead enthusiast.",
	}, cookies...)

	resp = doJSON(t, srv, http.MethodGet, "/api/users/alice", nil)
	var body map[string]any
	decode(t, resp, &body)
	if body["bio"] != "Mead enthusiast." {
		t.Errorf("public profile bio: got %v", body["bio"])
	}
}
