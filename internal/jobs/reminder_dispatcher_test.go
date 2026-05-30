package jobs

import (
	"bufio"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// TestSendEmail_DeliversToFakeSMTP verifies the dispatcher composes a valid
// SMTP message and delivers it via the configured relay. Asserts wire-level
// fields the dispatcher controls (From/To/Subject/body) — the rest is
// go-mail's job.
func TestSendEmail_DeliversToFakeSMTP(t *testing.T) {
	ctx := context.Background()
	srv := newFakeSMTP(t)
	defer srv.close()

	host, port := srv.addr()
	w := &reminderDispatchWorker{
		smtp: SMTPConfig{
			Host:    host,
			Port:    port,
			From:    "zymo@example.com",
			TLSMode: "none",
		},
	}
	w.sendEmail(ctx, "user@example.com", "Time to rack", "Batch FG looks stable.")

	select {
	case got := <-srv.msgs:
		if got.from != "zymo@example.com" {
			t.Errorf("From = %q", got.from)
		}
		if got.to != "user@example.com" {
			t.Errorf("To = %q", got.to)
		}
		if !strings.Contains(got.data, "Subject: Time to rack") {
			t.Errorf("DATA missing subject; got:\n%s", got.data)
		}
		if !strings.Contains(got.data, "Batch FG looks stable.") {
			t.Errorf("DATA missing body; got:\n%s", got.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake SMTP never received a message")
	}
}

// TestSendEmail_SwallowsConnectionFailure asserts that a relay we can't
// reach is logged and dropped — the in-app notification is the durable
// record, so a failed external send shouldn't surface as a job failure.
func TestSendEmail_SwallowsConnectionFailure(t *testing.T) {
	ctx := context.Background()
	// Bind a listener, capture the port, then immediately close so dialing
	// the address fails. Using port 0 + close avoids hard-coding a port.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := lis.Addr().(*net.TCPAddr)
	lis.Close()
	w := &reminderDispatchWorker{
		smtp: SMTPConfig{
			Host:    "127.0.0.1",
			Port:    addr.Port,
			From:    "zymo@example.com",
			TLSMode: "none",
		},
	}
	// Just asserting no panic.
	w.sendEmail(ctx, "user@example.com", "x", "y")
}

// TestDispatcher_SkipsEmailWhenDisabled drives the full Work() path with
// email_enabled=false and verifies SMTP is *not* dialed even though host is
// reachable.
func TestDispatcher_SkipsEmailWhenDisabled(t *testing.T) {
	ctx := context.Background()
	pool := testutil.Pool(t, ctx)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := queries.New(pool).WithTx(tx)

	user, err := q.CreateUser(ctx, queries.CreateUserParams{
		Username: "email_disabled",
		Email:    "email_disabled@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}

	srv := newFakeSMTP(t)
	defer srv.close()
	host, port := srv.addr()

	if _, err := q.UpsertNotificationPrefs(ctx, queries.UpsertNotificationPrefsParams{
		UserID:       user.ID,
		PushEnabled:  false,
		EmailEnabled: false, // <-- the switch we care about
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

	worker := &reminderDispatchWorker{
		queries: q,
		smtp: SMTPConfig{
			Host:    host,
			Port:    port,
			From:    "zymo@example.com",
			TLSMode: "none",
		},
	}
	if err := worker.Work(ctx, &river.Job[ReminderDispatchArgs]{}); err != nil {
		t.Fatalf("Work: %v", err)
	}

	if atomic.LoadInt32(&srv.connects) != 0 {
		t.Fatalf("smtp was dialed even though email_enabled=false (connects=%d)", srv.connects)
	}
}

// --- fake SMTP server -----------------------------------------------------

type capturedMessage struct {
	from string
	to   string
	data string
}

type fakeSMTPServer struct {
	lis      net.Listener
	msgs     chan capturedMessage
	connects int32
}

func newFakeSMTP(t *testing.T) *fakeSMTPServer {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTPServer{lis: lis, msgs: make(chan capturedMessage, 8)}
	go f.serve()
	return f
}

func (f *fakeSMTPServer) addr() (string, int) {
	a := f.lis.Addr().(*net.TCPAddr)
	return "127.0.0.1", a.Port
}

func (f *fakeSMTPServer) close() { f.lis.Close() }

func (f *fakeSMTPServer) serve() {
	for {
		conn, err := f.lis.Accept()
		if err != nil {
			return
		}
		atomic.AddInt32(&f.connects, 1)
		go f.handle(conn)
	}
}

func (f *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	rd := bufio.NewReader(conn)
	write := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	write("220 fake.example.com ESMTP")

	var msg capturedMessage
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-fake.example.com")
			write("250 8BITMIME")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			msg.from = extractAngleAddr(line[10:])
			write("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			msg.to = extractAngleAddr(line[8:])
			write("250 OK")
		case upper == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>")
			var buf strings.Builder
			for {
				dline, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				trimmed := strings.TrimRight(dline, "\r\n")
				if trimmed == "." {
					break
				}
				// Un-stuff leading "." per RFC 5321 §4.5.2.
				if strings.HasPrefix(trimmed, "..") {
					trimmed = trimmed[1:]
				}
				buf.WriteString(trimmed)
				buf.WriteString("\n")
			}
			msg.data = buf.String()
			f.msgs <- msg
			msg = capturedMessage{}
			write("250 OK")
		case upper == "QUIT":
			write("221 BYE")
			return
		case upper == "RSET":
			msg = capturedMessage{}
			write("250 OK")
		case upper == "NOOP":
			write("250 OK")
		default:
			write("250 OK")
		}
	}
}

// extractAngleAddr pulls "user@example.com" out of "<user@example.com> BODY=8BITMIME".
// Real-world MAIL FROM / RCPT TO commands often carry ESMTP extensions after
// the angle-addr, so a naive Trim("<>") leaves the suffix attached.
func extractAngleAddr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s[i+1:], ">"); j >= 0 {
			return s[i+1 : i+1+j]
		}
	}
	return strings.SplitN(s, " ", 2)[0]
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
