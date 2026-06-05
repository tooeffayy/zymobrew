package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
	"zymobrew/internal/testutil"
)

// Distinctive secret values we can grep the response for. None of these
// substrings may appear in the admin config JSON.
const (
	secretDBURL     = "postgres://zymo:SUPERSECRETDBPASS@db:5432/zymo"
	secretDBPass    = "SUPERSECRETDBPASS"
	secretVAPIDPriv = "VAPIDPRIVATEKEY-secret-123"
	secretSMTPPass  = "SMTPPASSWORD-secret-456"
	secretS3Secret  = "S3SECRETKEY-secret-789"
	secretS3Access  = "AKIAEXAMPLEACCESSKEY01"
)

func adminConfigTestServer(t *testing.T) (*server.Server, []*http.Cookie) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	if _, err := pool.Exec(ctx, "TRUNCATE users, sessions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	cfg := config.Config{
		InstanceMode:    config.ModeOpen,
		DatabaseURL:     secretDBURL,
		VAPIDPublicKey:  "vapid-public",
		VAPIDPrivateKey: secretVAPIDPriv,
		SMTPHost:        "smtp.example.com",
		SMTPFrom:        "zymo@example.com",
		SMTPPassword:    secretSMTPPass,
		SMTPTLSMode:     "starttls",
		StorageBackend:  "s3",
		S3AccessKey:     secretS3Access,
		S3SecretKey:     secretS3Secret,
	}
	srv := server.New(pool, cfg, nil, nil)

	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "admin", "email": "admin@example.com", "password": "supersecret",
	})
	cookies := resp.Cookies()
	if _, err := pool.Exec(ctx, "UPDATE users SET is_admin = TRUE WHERE username = 'admin'"); err != nil {
		t.Fatal(err)
	}
	return srv, cookies
}

func TestAdminConfig_NoSecretsLeak(t *testing.T) {
	srv, cookies := adminConfigTestServer(t)

	resp := doJSON(t, srv, http.MethodGet, "/api/admin/config", nil, cookies...)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin config: got %d, want 200", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)

	// No secret value — and no full S3 access key ID — may appear verbatim.
	for _, secret := range []string{
		secretDBURL, secretDBPass, secretVAPIDPriv, secretSMTPPass, secretS3Secret, secretS3Access,
	} {
		if strings.Contains(body, secret) {
			t.Errorf("response leaked secret %q\nbody: %s", secret, body)
		}
	}

	// The S3 access key ID should appear only as a fixed-width tail mask.
	wantMask := "••••" + secretS3Access[len(secretS3Access)-4:]
	if !strings.Contains(body, wantMask) {
		t.Errorf("expected masked S3 access key %q in body: %s", wantMask, body)
	}

	// Presence booleans report true for every configured secret.
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"database_url_set", "vapid_private_key_set", "smtp_password_set", "s3_secret_key_set",
	} {
		if view[k] != true {
			t.Errorf("%s = %v, want true", k, view[k])
		}
	}
	if view["push_configured"] != true {
		t.Errorf("push_configured = %v, want true", view["push_configured"])
	}
	if view["email_configured"] != true {
		t.Errorf("email_configured = %v, want true", view["email_configured"])
	}
	if view["storage_backend"] != "s3" {
		t.Errorf("storage_backend = %v, want s3", view["storage_backend"])
	}
}

func TestAdminConfig_RequiresAdmin(t *testing.T) {
	srv, _ := setupAuth(t, config.ModeOpen)

	// Unauthenticated → 401.
	resp := doJSON(t, srv, http.MethodGet, "/api/admin/config", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth: got %d, want 401", resp.StatusCode)
	}

	// Authenticated but non-admin → 403.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": "bob", "email": "bob@example.com", "password": "supersecret",
	})
	cookies := resp.Cookies()
	resp = doJSON(t, srv, http.MethodGet, "/api/admin/config", nil, cookies...)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin: got %d, want 403", resp.StatusCode)
	}
}
