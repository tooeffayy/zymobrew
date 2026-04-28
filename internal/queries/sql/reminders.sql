-- name: CreateReminder :one
INSERT INTO reminders (batch_id, user_id, title, description, fire_at, suggested_event_kind)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetReminder :one
SELECT * FROM reminders WHERE id = $1 AND user_id = $2;

-- name: ListBatchReminders :many
SELECT * FROM reminders
WHERE batch_id = $1 AND user_id = $2
ORDER BY fire_at ASC;

-- name: UpdateReminder :one
UPDATE reminders SET
  title                = COALESCE(sqlc.narg('title'),                title),
  description          = COALESCE(sqlc.narg('description'),          description),
  fire_at              = COALESCE(sqlc.narg('fire_at'),              fire_at),
  status               = COALESCE(sqlc.narg('status'),               status),
  suggested_event_kind = COALESCE(sqlc.narg('suggested_event_kind'), suggested_event_kind)
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id')
RETURNING *;

-- name: CancelReminder :execrows
UPDATE reminders SET status = 'cancelled'
WHERE id = $1 AND user_id = $2 AND status NOT IN ('fired', 'completed');

-- name: ClaimDueReminders :many
UPDATE reminders
SET status = 'fired', fired_at = now()
WHERE id IN (
  SELECT id FROM reminders
  WHERE status = 'scheduled' AND fire_at <= now()
  ORDER BY fire_at
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;
