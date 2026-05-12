package jobs

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"

	"zymobrew/internal/queries"
	"zymobrew/internal/testutil"
)

// TestSendPush_DeletesDeviceOnRevokedStatus verifies that a 410 Gone or 404
// Not Found response from the push service causes the corresponding
// push_devices row to be deleted, so future ticks don't retry a permanently
// dead subscription.
func TestSendPush_DeletesDeviceOnRevokedStatus(t *testing.T) {
	cases := []struct {
		name string
		code int
	}{
		{"410_gone", http.StatusGone},
		{"404_not_found", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSendPushCase(t, tc.code, true /* expectDeleted */)
		})
	}
}

// TestSendPush_KeepsDeviceOnSuccess verifies a healthy 201 response leaves
// the row intact.
func TestSendPush_KeepsDeviceOnSuccess(t *testing.T) {
	runSendPushCase(t, http.StatusCreated, false /* expectDeleted */)
}

// TestSendPush_KeepsDeviceOnTransientFailure verifies a 5xx response leaves
// the row intact — transient push-service failures shouldn't drop subscriptions.
func TestSendPush_KeepsDeviceOnTransientFailure(t *testing.T) {
	runSendPushCase(t, http.StatusServiceUnavailable, false /* expectDeleted */)
}

func runSendPushCase(t *testing.T, status int, expectDeleted bool) {
	t.Helper()
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := queries.New(pool).WithTx(tx)

	user, err := q.CreateUser(ctx, queries.CreateUserParams{
		Username: "push_test_" + t.Name(),
		Email:    "push_test_" + t.Name() + "@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	defer srv.Close()

	p256dh, auth := genSubscriberKeys(t)
	vapidPriv, vapidPub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}

	dev, err := q.UpsertPushDevice(ctx, queries.UpsertPushDeviceParams{
		UserID:   user.ID,
		Platform: "web",
		Token:    srv.URL,
		P256dh:   pgtype.Text{String: p256dh, Valid: true},
		Auth:     pgtype.Text{String: auth, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	worker := &reminderDispatchWorker{
		queries:      q,
		vapidPub:     vapidPub,
		vapidPriv:    vapidPriv,
		vapidSubject: "mailto:test@example.com",
	}
	worker.sendPush(ctx, dev, []byte(`{"title":"x","body":"y","url_path":""}`))

	devs, err := q.ListPushDevicesForUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if expectDeleted && len(devs) != 0 {
		t.Fatalf("expected device deleted on %d, got %d remaining", status, len(devs))
	}
	if !expectDeleted && len(devs) != 1 {
		t.Fatalf("expected device kept on %d, got %d remaining", status, len(devs))
	}
}

// TestSendApprise_PostsExpectedPayload verifies the dispatcher POSTs the
// expected JSON to the Apprise sidecar with the user's per-account URL.
func TestSendApprise_PostsExpectedPayload(t *testing.T) {
	ctx := context.Background()

	var got struct {
		URLs  string `json:"urls"`
		Title string `json:"title"`
		Body  string `json:"body"`
		Type  string `json:"type"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/notify" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			http.Error(w, "wrong content-type", http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	w := &reminderDispatchWorker{appriseAPIURL: srv.URL}
	w.sendApprise(ctx, "tgram://bottoken/12345", "Time to rack", "Batch FG looks stable.")

	if got.URLs != "tgram://bottoken/12345" {
		t.Errorf("urls = %q, want tgram://bottoken/12345", got.URLs)
	}
	if got.Title != "Time to rack" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Body != "Batch FG looks stable." {
		t.Errorf("body = %q", got.Body)
	}
	if got.Type != "info" {
		t.Errorf("type = %q, want info", got.Type)
	}
}

// TestSendApprise_SwallowsRejection asserts a 4xx response from Apprise is
// logged and dropped — the in-app notification is the durable record, so a
// rejected external send shouldn't surface as a job failure.
func TestSendApprise_SwallowsRejection(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"unknown scheme"}`))
	}))
	defer srv.Close()
	w := &reminderDispatchWorker{appriseAPIURL: srv.URL}
	// Just asserting no panic, no return value to check.
	w.sendApprise(ctx, "bogus://nope", "x", "y")
}

// TestDispatcher_SkipsAppriseWhenDisabled drives the full Work() path to
// confirm that Apprise is *not* called when apprise_enabled=false even though
// the URL is set.
func TestDispatcher_SkipsAppriseWhenDisabled(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := queries.New(pool).WithTx(tx)

	user, err := q.CreateUser(ctx, queries.CreateUserParams{
		Username: "apprise_disabled",
		Email:    "apprise_disabled@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := q.UpsertNotificationPrefs(ctx, queries.UpsertNotificationPrefsParams{
		UserID:         user.ID,
		PushEnabled:    false,
		AppriseEnabled: false, // <-- the switch we care about
		AppriseUrl:     pgtype.Text{String: "mailto://noop", Valid: true},
		Timezone:       "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := q.CreateReminder(ctx, queries.CreateReminderParams{
		UserID:      user.ID,
		Title:       "x",
		Description: pgtype.Text{String: "y", Valid: true},
		FireAt:      pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	worker := &reminderDispatchWorker{queries: q, appriseAPIURL: srv.URL}
	if err := worker.Work(ctx, &river.Job[ReminderDispatchArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if called {
		t.Fatal("Apprise was called even though apprise_enabled=false")
	}
}

// genSubscriberKeys produces a valid raw P-256 public key (uncompressed, 65
// bytes) and 16-byte auth secret, both base64-url-encoded — the shape
// browsers send to the server when subscribing to push.
func genSubscriberKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authBytes := make([]byte, 16)
	if _, err := rand.Read(authBytes); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		base64.RawURLEncoding.EncodeToString(authBytes)
}
