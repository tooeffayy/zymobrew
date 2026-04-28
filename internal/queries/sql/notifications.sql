-- name: CreateNotification :one
INSERT INTO notifications (user_id, reminder_id, kind, title, body, url_path)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListNotifications :many
SELECT * FROM notifications
WHERE user_id = $1
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

-- name: MarkNotificationRead :execrows
UPDATE notifications SET read_at = now()
WHERE id = $1 AND user_id = $2 AND read_at IS NULL;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read_at = now()
WHERE user_id = $1 AND read_at IS NULL;

-- name: GetNotificationPrefs :one
SELECT * FROM notification_prefs WHERE user_id = $1;

-- name: UpsertNotificationPrefs :one
INSERT INTO notification_prefs (user_id, push_enabled, email_enabled, quiet_hours_start, quiet_hours_end, timezone)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (user_id) DO UPDATE SET
  push_enabled      = EXCLUDED.push_enabled,
  email_enabled     = EXCLUDED.email_enabled,
  quiet_hours_start = EXCLUDED.quiet_hours_start,
  quiet_hours_end   = EXCLUDED.quiet_hours_end,
  timezone          = EXCLUDED.timezone
RETURNING *;
