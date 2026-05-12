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

	"zymobrew/internal/queries"
)

const reminderClaimBatch = 100

// ReminderDispatchArgs is the (empty) argument set for the periodic reminder
// dispatcher. Exported so tests can construct the job directly.
type ReminderDispatchArgs struct{}

func (ReminderDispatchArgs) Kind() string { return "reminder_dispatcher" }

type reminderDispatchWorker struct {
	river.WorkerDefaults[ReminderDispatchArgs]
	queries       *queries.Queries
	vapidPub      string
	vapidPriv     string
	vapidSubject  string
	appriseAPIURL string
}

func (w *reminderDispatchWorker) pushConfigured() bool {
	return w.vapidPub != "" && w.vapidPriv != ""
}

func (w *reminderDispatchWorker) appriseConfigured() bool {
	return w.appriseAPIURL != ""
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

	// Cache prefs and devices per user to avoid N+1 queries.
	prefCache := map[uuid.UUID]queries.NotificationPref{}
	devCache := map[uuid.UUID][]queries.PushDevice{}

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

		// External channels (push, Apprise) are best-effort. If neither is
		// configured on this instance we can skip the prefs lookup entirely.
		if !w.pushConfigured() && !w.appriseConfigured() {
			continue
		}

		prefs, err := w.userPrefs(ctx, r.UserID, prefCache)
		if err != nil || isInQuietHours(prefs, now) {
			continue
		}

		body := textVal(r.Description)
		urlPathStr := textVal(urlPath)

		if w.appriseConfigured() && prefs.AppriseEnabled && prefs.AppriseUrl.Valid && prefs.AppriseUrl.String != "" {
			w.sendApprise(ctx, prefs.AppriseUrl.String, r.Title, body)
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

func (w *reminderDispatchWorker) userPrefs(ctx context.Context, userID uuid.UUID, cache map[uuid.UUID]queries.NotificationPref) (queries.NotificationPref, error) {
	if p, ok := cache[userID]; ok {
		return p, nil
	}
	p, err := w.queries.GetNotificationPrefs(ctx, userID)
	if err != nil {
		// No prefs row = defaults: push enabled, no quiet hours.
		p = queries.NotificationPref{UserID: userID, PushEnabled: true, Timezone: "UTC"}
	}
	cache[userID] = p
	return p, nil
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
func isInQuietHours(prefs queries.NotificationPref, now time.Time) bool {
	if !prefs.QuietHoursStart.Valid || !prefs.QuietHoursEnd.Valid {
		return false
	}
	loc, err := time.LoadLocation(prefs.Timezone)
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
