-- name: CreateReminderTemplate :one
INSERT INTO recipe_reminder_templates (recipe_id, title, description, anchor, offset_minutes, suggested_event_kind, sort_order)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetReminderTemplate :one
SELECT * FROM recipe_reminder_templates WHERE id = $1;

-- name: ListReminderTemplates :many
SELECT * FROM recipe_reminder_templates
WHERE recipe_id = $1
ORDER BY sort_order ASC, id ASC;

-- name: UpdateReminderTemplate :one
UPDATE recipe_reminder_templates SET
  title                = COALESCE(sqlc.narg('title'),                title),
  description          = COALESCE(sqlc.narg('description'),          description),
  anchor               = COALESCE(sqlc.narg('anchor'),               anchor),
  offset_minutes       = COALESCE(sqlc.narg('offset_minutes'),       offset_minutes),
  suggested_event_kind = COALESCE(sqlc.narg('suggested_event_kind'), suggested_event_kind),
  sort_order           = COALESCE(sqlc.narg('sort_order'),           sort_order)
WHERE id = sqlc.arg('id') AND recipe_id = sqlc.arg('recipe_id')
RETURNING *;

-- name: DeleteReminderTemplate :execrows
DELETE FROM recipe_reminder_templates WHERE id = $1 AND recipe_id = $2;

-- name: MaterializeReminderTemplates :exec
INSERT INTO reminders (batch_id, user_id, template_id, title, description, fire_at, suggested_event_kind)
SELECT
  sqlc.arg('batch_id')::uuid,
  sqlc.arg('user_id')::uuid,
  t.id,
  t.title,
  t.description,
  sqlc.arg('anchor_time')::timestamptz + (t.offset_minutes * INTERVAL '1 minute'),
  t.suggested_event_kind
FROM recipe_reminder_templates t
WHERE t.recipe_id = sqlc.arg('recipe_id')::uuid
  AND t.anchor = sqlc.arg('anchor')::reminder_anchor
  AND NOT EXISTS (
    SELECT 1 FROM reminders r
    WHERE r.template_id = t.id
      AND r.batch_id = sqlc.arg('batch_id')::uuid
      AND r.status NOT IN ('cancelled', 'dismissed')
  );

-- name: ReanchorReminders :exec
-- Shifts fire_at on already-materialized reminders when the anchor moves
-- (e.g. batch.started_at is patched). Status filter is intentionally narrower
-- than MaterializeReminderTemplates' NOT EXISTS guard: only 'scheduled' rows
-- are rescheduled. Don't un-fire a fired reminder, and don't yank a snoozed
-- reminder's wake time out from under the user.
UPDATE reminders SET
  fire_at = sqlc.arg('anchor_time')::timestamptz + (t.offset_minutes * INTERVAL '1 minute')
FROM recipe_reminder_templates t
WHERE reminders.template_id = t.id
  AND reminders.batch_id = sqlc.arg('batch_id')::uuid
  AND t.recipe_id = sqlc.arg('recipe_id')::uuid
  AND t.anchor = sqlc.arg('anchor')::reminder_anchor
  AND reminders.status = 'scheduled';
