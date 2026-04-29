package jobs

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/jackc/pgx/v5/pgtype"

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
