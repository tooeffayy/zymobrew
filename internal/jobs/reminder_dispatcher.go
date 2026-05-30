package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/riverqueue/river"
	mail "github.com/wneessen/go-mail"

	"zymobrew/internal/apprise"
	"zymobrew/internal/queries"
)

// SMTPConfig collapses the dispatcher's SMTP knobs. Zero-value Host disables
// the email channel — the toggle still flips in prefs, but no mail is sent
// (mirrors the AppriseAPIURL contract).
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLSMode  string // "starttls" | "tls" | "none"
}

const reminderClaimBatch = 100

// ReminderDispatchArgs is the (empty) argument set for the periodic reminder
// dispatcher. Exported so tests can construct the job directly.
type ReminderDispatchArgs struct{}

func (ReminderDispatchArgs) Kind() string { return "reminder_dispatcher" }

type reminderDispatchWorker struct {
	river.WorkerDefaults[ReminderDispatchArgs]
	queries          *queries.Queries
	vapidPub         string
	vapidPriv        string
	vapidSubject     string
	appriseAPIURL    string
	appriseValidator *apprise.Validator
	smtp             SMTPConfig
}

func (w *reminderDispatchWorker) pushConfigured() bool {
	return w.vapidPub != "" && w.vapidPriv != ""
}

func (w *reminderDispatchWorker) appriseConfigured() bool {
	return w.appriseAPIURL != ""
}

func (w *reminderDispatchWorker) emailConfigured() bool {
	return w.smtp.Host != "" && w.smtp.From != ""
}

// Work claims all due reminders atomically, creates in-app notifications, and
// sends web-push notifications to devices whose prefs allow it (quiet hours
// respected). Push errors are logged but do not fail the job.
func (w *reminderDispatchWorker) Work(ctx context.Context, _ *river.Job[ReminderDispatchArgs]) error {
	due, err := w.queries.ClaimDueReminders(ctx, reminderClaimBatch)
	if err != nil {
		return fmt.Errorf("claim due reminders: %w", err)
	}
	if len(due) == 0 {
		return nil
	}

	// Cache prefs, timezone, devices, and emails per user to avoid N+1 queries.
	prefCache := map[uuid.UUID]queries.NotificationPref{}
	tzCache := map[uuid.UUID]string{}
	devCache := map[uuid.UUID][]queries.PushDevice{}
	emailCache := map[uuid.UUID]string{}

	now := time.Now()

	for _, r := range due {
		var reminderID uuid.NullUUID
		reminderID.UUID = r.ID
		reminderID.Valid = true

		var urlPath pgtype.Text
		if r.BatchID.Valid {
			urlPath = pgtype.Text{String: "/batches/" + r.BatchID.UUID.String(), Valid: true}
		}

		// Always create the in-app notification.
		if _, err := w.queries.CreateNotification(ctx, queries.CreateNotificationParams{
			UserID:     r.UserID,
			ReminderID: reminderID,
			Kind:       "reminder",
			Title:      r.Title,
			Body:       r.Description,
			UrlPath:    urlPath,
		}); err != nil {
			slog.Error("create notification", "reminder_id", r.ID, "err", err)
		}

		// External channels (push, Apprise, SMTP) are best-effort. If none is
		// configured on this instance we can skip the prefs lookup entirely.
		if !w.pushConfigured() && !w.appriseConfigured() && !w.emailConfigured() {
			continue
		}

		prefs, err := w.userPrefs(ctx, r.UserID, prefCache)
		if err != nil {
			continue
		}
		tz := w.userTimezone(ctx, r.UserID, tzCache)
		if isInQuietHours(prefs, tz, now) {
			continue
		}

		body := textVal(r.Description)
		urlPathStr := textVal(urlPath)

		if w.appriseConfigured() && prefs.AppriseEnabled && prefs.AppriseUrl.Valid && prefs.AppriseUrl.String != "" {
			// Re-validate the saved URL against current policy. Rows
			// persisted before the SSRF fix shipped, or saved when the
			// operator had AppriseAllowWebhookSchemes=true and later flipped
			// it off, would otherwise keep firing. Failure is logged and
			// dropped — the user can fix it via the prefs UI.
			if err := w.appriseValidator.Validate(ctx, prefs.AppriseUrl.String); err != nil {
				slog.Warn("apprise url failed policy", "user_id", r.UserID, "err", err)
			} else {
				w.sendApprise(ctx, prefs.AppriseUrl.String, r.Title, body)
			}
		}

		if w.emailConfigured() && prefs.EmailEnabled {
			if addr, err := w.userEmail(ctx, r.UserID, emailCache); err == nil && addr != "" {
				w.sendEmail(ctx, addr, r.Title, body)
			}
		}

		if !w.pushConfigured() || !prefs.PushEnabled {
			continue
		}

		devices, err := w.userDevices(ctx, r.UserID, devCache)
		if err != nil {
			slog.Error("list push devices", "user_id", r.UserID, "err", err)
			continue
		}

		payload, _ := json.Marshal(map[string]string{
			"title":    r.Title,
			"body":     body,
			"url_path": urlPathStr,
		})

		for _, dev := range devices {
			if !dev.P256dh.Valid || !dev.Auth.Valid {
				continue
			}
			w.sendPush(ctx, dev, payload)
		}
	}
	return nil
}

// sendApprise POSTs a notification to the Apprise sidecar's stateless /notify
// endpoint, routing to the user's per-account Apprise URL. Errors are logged
// and swallowed — the in-app notification has already been written, which is
// the durable record.
func (w *reminderDispatchWorker) sendApprise(ctx context.Context, target, title, body string) {
	payload, _ := json.Marshal(map[string]any{
		"urls":  target,
		"title": title,
		"body":  body,
		"type":  "info",
	})
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.appriseAPIURL+"/notify", bytes.NewReader(payload))
	if err != nil {
		slog.Error("apprise build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("apprise send", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		slog.Warn("apprise rejected", "status", resp.StatusCode, "body", strings.TrimSpace(string(respBody)))
	}
}

// sendEmail dials the configured SMTP relay and delivers a plaintext message.
// Errors are logged and dropped — the in-app notification is already written,
// and a failed external send shouldn't surface as a job failure that would
// retry-storm a misconfigured relay.
func (w *reminderDispatchWorker) sendEmail(ctx context.Context, toAddr, title, body string) {
	opts := []mail.Option{
		mail.WithPort(w.smtp.Port),
		mail.WithTimeout(15 * time.Second),
	}
	switch w.smtp.TLSMode {
	case "tls":
		opts = append(opts, mail.WithSSL())
	case "none":
		opts = append(opts, mail.WithTLSPolicy(mail.NoTLS))
	default: // "starttls"
		opts = append(opts, mail.WithTLSPolicy(mail.TLSMandatory))
	}
	if w.smtp.Username != "" {
		// PLAIN over TLS covers the common self-hosted-relay + transactional-
		// SMTP cases (Postmark, SendGrid, Mailgun, Postfix with SASL). Servers
		// requiring LOGIN/XOAUTH2 specifically would need a knob; not adding
		// one until a user actually hits that wall.
		opts = append(opts,
			mail.WithSMTPAuth(mail.SMTPAuthPlain),
			mail.WithUsername(w.smtp.Username),
			mail.WithPassword(w.smtp.Password),
		)
	}
	client, err := mail.NewClient(w.smtp.Host, opts...)
	if err != nil {
		slog.Error("smtp client", "err", err)
		return
	}
	msg := mail.NewMsg()
	if err := msg.From(w.smtp.From); err != nil {
		slog.Error("smtp from", "err", err)
		return
	}
	if err := msg.To(toAddr); err != nil {
		slog.Error("smtp to", "addr", toAddr, "err", err)
		return
	}
	msg.Subject(title)
	msg.SetBodyString(mail.TypeTextPlain, body)
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := client.DialAndSendWithContext(dialCtx, msg); err != nil {
		slog.Error("smtp send", "addr", toAddr, "err", err)
	}
}

func (w *reminderDispatchWorker) userEmail(ctx context.Context, userID uuid.UUID, cache map[uuid.UUID]string) (string, error) {
	if v, ok := cache[userID]; ok {
		return v, nil
	}
	u, err := w.queries.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	cache[userID] = u.Email
	return u.Email, nil
}

func (w *reminderDispatchWorker) userPrefs(ctx context.Context, userID uuid.UUID, cache map[uuid.UUID]queries.NotificationPref) (queries.NotificationPref, error) {
	if p, ok := cache[userID]; ok {
		return p, nil
	}
	p, err := w.queries.GetNotificationPrefs(ctx, userID)
	if err != nil {
		// No prefs row = defaults: push enabled, no quiet hours.
		p = queries.NotificationPref{UserID: userID, PushEnabled: true}
	}
	cache[userID] = p
	return p, nil
}

// userTimezone resolves the IANA timezone for quiet-hours interpretation.
// Cached per-tick alongside the other per-user lookups.
func (w *reminderDispatchWorker) userTimezone(ctx context.Context, userID uuid.UUID, cache map[uuid.UUID]string) string {
	if tz, ok := cache[userID]; ok {
		return tz
	}
	up, err := w.queries.GetUserPrefs(ctx, userID)
	tz := "UTC"
	if err == nil && up.Timezone != "" {
		tz = up.Timezone
	}
	cache[userID] = tz
	return tz
}

func (w *reminderDispatchWorker) userDevices(ctx context.Context, userID uuid.UUID, cache map[uuid.UUID][]queries.PushDevice) ([]queries.PushDevice, error) {
	if devs, ok := cache[userID]; ok {
		return devs, nil
	}
	devs, err := w.queries.ListPushDevicesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	cache[userID] = devs
	return devs, nil
}

func (w *reminderDispatchWorker) sendPush(ctx context.Context, dev queries.PushDevice, payload []byte) {
	sub := &webpush.Subscription{
		Endpoint: dev.Token,
		Keys:     webpush.Keys{P256dh: dev.P256dh.String, Auth: dev.Auth.String},
	}
	resp, err := webpush.SendNotification(payload, sub, &webpush.Options{
		HTTPClient:      &http.Client{Timeout: 10 * time.Second},
		TTL:             86400,
		Subscriber:      w.vapidSubject,
		VAPIDPublicKey:  w.vapidPub,
		VAPIDPrivateKey: w.vapidPriv,
		Urgency:         webpush.UrgencyNormal,
	})
	if err != nil {
		slog.Error("web push send", "endpoint", dev.Token, "err", err)
		return
	}
	defer resp.Body.Close()

	// 404/410 mean the subscription has been permanently revoked by the push
	// service (browser uninstalled, user blocked, expired). Drop the row so
	// future ticks don't keep retrying it.
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusNotFound {
		if _, err := w.queries.DeletePushDevice(ctx, queries.DeletePushDeviceParams{
			UserID: dev.UserID,
			Token:  dev.Token,
		}); err != nil {
			slog.Error("delete revoked push device", "user_id", dev.UserID, "endpoint", dev.Token, "err", err)
		}
		return
	}
	if resp.StatusCode >= 400 {
		slog.Warn("web push rejected", "endpoint", dev.Token, "status", resp.StatusCode)
	}
}

// isInQuietHours reports whether now (in the user's timezone) falls within
// the configured quiet hours window. Handles windows that wrap midnight
// (e.g. 22:00–06:00).
func isInQuietHours(prefs queries.NotificationPref, timezone string, now time.Time) bool {
	if !prefs.QuietHoursStart.Valid || !prefs.QuietHoursEnd.Valid {
		return false
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return false
	}
	local := now.In(loc)
	todMicros := int64(local.Hour())*3600000000 + int64(local.Minute())*60000000
	start := prefs.QuietHoursStart.Microseconds
	end := prefs.QuietHoursEnd.Microseconds
	if start <= end {
		return todMicros >= start && todMicros < end
	}
	// Wraps midnight
	return todMicros >= start || todMicros < end
}

func textVal(t pgtype.Text) string {
	if t.Valid {
		return t.String
	}
	return ""
}
