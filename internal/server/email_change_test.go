package server_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"zymobrew/internal/config"
	"zymobrew/internal/server"
	"zymobrew/internal/testutil"
)

type sentMail struct{ To, Subject, Body string }

// emailChangeServer builds a server with SMTP "configured" and a capturing
// mailer, so the email-change flow runs end-to-end without a live relay. The
// returned slice (guarded by mu) records every message the handler tried to
// send; tests pull confirm/cancel tokens out of the bodies.
func emailChangeServer(t *testing.T) (*server.Server, *[]sentMail, *sync.Mutex) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)
	if _, err := pool.Exec(ctx, "TRUNCATE users, sessions CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	cfg := config.Config{
		InstanceMode: config.ModeOpen,
		SMTPHost:     "smtp.test",
		SMTPFrom:     "zymo@test",
		SMTPPort:     587,
		SMTPTLSMode:  "starttls",
		BaseURL:      "https://zymo.test",
	}
	srv := server.New(pool, cfg, nil, nil)

	var mu sync.Mutex
	var sent []sentMail
	srv.SetMailerForTest(func(_ context.Context, _ config.Config, to, subject, body string) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, sentMail{To: to, Subject: subject, Body: body})
		return nil
	})
	return srv, &sent, &mu
}

var tokenRE = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

// tokenForRecipient returns the token embedded in the link of the most recent
// mail addressed to `to`.
func tokenForRecipient(t *testing.T, sent *[]sentMail, mu *sync.Mutex, to string) string {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	for i := len(*sent) - 1; i >= 0; i-- {
		m := (*sent)[i]
		if m.To != to {
			continue
		}
		match := tokenRE.FindStringSubmatch(m.Body)
		if match == nil {
			t.Fatalf("no token in mail to %s: %q", to, m.Body)
		}
		return match[1]
	}
	t.Fatalf("no mail sent to %s", to)
	return ""
}

func registerForEmailChange(t *testing.T, srv *server.Server, username, email string) []*http.Cookie {
	t.Helper()
	resp := doJSON(t, srv, http.MethodPost, "/api/auth/register", map[string]string{
		"username": username, "email": email, "password": "supersecret",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: got %d", username, resp.StatusCode)
	}
	return resp.Cookies()
}

func TestEmailChange_ConfirmFlow(t *testing.T) {
	srv, sent, mu := emailChangeServer(t)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")

	// Initiate: correct password → 202 pending, email NOT yet changed.
	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
		"current_password": "supersecret",
		"email":            "alice+new@example.com",
	}, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("initiate: got %d, want 202", resp.StatusCode)
	}

	// Two mails: confirmation to the new address, notice to the old.
	mu.Lock()
	if len(*sent) != 2 {
		t.Fatalf("expected 2 mails, got %d", len(*sent))
	}
	mu.Unlock()

	// Old email still works for login; new one does not yet.
	if resp := doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice+new@example.com", "password": "supersecret",
	}); resp.StatusCode == http.StatusOK {
		t.Fatal("new email should not work before confirmation")
	}

	// Confirm via the token from the new-address mail (no session needed).
	token := tokenForRecipient(t, sent, mu, "alice+new@example.com")
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/email/confirm", map[string]string{"token": token})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm: got %d, want 200", resp.StatusCode)
	}
	var view map[string]any
	decode(t, resp, &view)
	if view["email"] != "alice+new@example.com" {
		t.Errorf("email after confirm: got %v", view["email"])
	}

	// New email now logs in; the token is single-use (row deleted).
	if resp := doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice+new@example.com", "password": "supersecret",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("login with new email: got %d", resp.StatusCode)
	}
	if resp := doJSON(t, srv, http.MethodPost, "/api/auth/email/confirm", map[string]string{"token": token}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("reused token: got %d, want 400", resp.StatusCode)
	}
}

func TestEmailChange_CancelFromOldAddress(t *testing.T) {
	srv, sent, mu := emailChangeServer(t)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
		"current_password": "supersecret", "email": "alice+new@example.com",
	}, cookies...)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("initiate: got %d", resp.StatusCode)
	}

	// Cancel using the token mailed to the OLD address.
	cancelToken := tokenForRecipient(t, sent, mu, "alice@example.com")
	confirmToken := tokenForRecipient(t, sent, mu, "alice+new@example.com")

	resp = doJSON(t, srv, http.MethodPost, "/api/auth/email/cancel", map[string]string{"token": cancelToken})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel: got %d, want 200", resp.StatusCode)
	}

	// After cancel, the confirm token is dead.
	resp = doJSON(t, srv, http.MethodPost, "/api/auth/email/confirm", map[string]string{"token": confirmToken})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("confirm after cancel: got %d, want 400", resp.StatusCode)
	}

	// Email unchanged: old address still logs in.
	if resp := doJSON(t, srv, http.MethodPost, "/api/auth/login", map[string]string{
		"identifier": "alice@example.com", "password": "supersecret",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("old email login after cancel: got %d", resp.StatusCode)
	}
}

func TestEmailChange_WrongPassword(t *testing.T) {
	srv, sent, mu := emailChangeServer(t)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
		"current_password": "wrongpass", "email": "alice+new@example.com",
	}, cookies...)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d, want 401", resp.StatusCode)
	}
	mu.Lock()
	if len(*sent) != 0 {
		t.Errorf("no mail should be sent on failed re-auth, got %d", len(*sent))
	}
	mu.Unlock()
}

func TestEmailChange_Collision(t *testing.T) {
	srv, _, _ := emailChangeServer(t)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")
	registerForEmailChange(t, srv, "bob", "bob@example.com")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
		"current_password": "supersecret", "email": "bob@example.com",
	}, cookies...)
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("collision: got %d, want 409", resp.StatusCode)
	}
}

func TestEmailChange_InvalidEmailRejected(t *testing.T) {
	srv, sent, mu := emailChangeServer(t)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")

	for _, bad := range []string{"notanemail", "Alice <a@b.com>", "a@b@c", ""} {
		resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
			"current_password": "supersecret", "email": bad,
		}, cookies...)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("email %q: got %d, want 400", bad, resp.StatusCode)
		}
	}
	mu.Lock()
	if len(*sent) != 0 {
		t.Errorf("no mail should be sent for invalid addresses, got %d", len(*sent))
	}
	mu.Unlock()
}

func TestEmailChange_RateLimited(t *testing.T) {
	srv, _, _ := emailChangeServer(t)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")

	// Burst is 5; distinct target addresses keep every call past validation
	// so only the limiter can reject. The 6th initiation should be throttled.
	var got429 bool
	for i := 0; i < 6; i++ {
		resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
			"current_password": "supersecret",
			"email":            fmt.Sprintf("alice+%d@example.com", i),
		}, cookies...)
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			if resp.Header.Get("Retry-After") == "" {
				t.Error("429 missing Retry-After header")
			}
			break
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("initiate %d: got %d, want 202", i, resp.StatusCode)
		}
	}
	if !got429 {
		t.Error("expected a 429 within the burst+1 window")
	}
}

func TestEmailChange_SMTPUnconfigured(t *testing.T) {
	// Default setupAuth config has no SMTP → the flow is unavailable.
	srv, _ := setupAuth(t, config.ModeOpen)
	cookies := registerForEmailChange(t, srv, "alice", "alice@example.com")

	resp := doJSON(t, srv, http.MethodPost, "/api/users/me/email", map[string]string{
		"current_password": "supersecret", "email": "alice+new@example.com",
	}, cookies...)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("no smtp: got %d, want 503", resp.StatusCode)
	}
}
