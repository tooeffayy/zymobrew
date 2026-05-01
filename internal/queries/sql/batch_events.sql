-- name: CreateBatchEvent :one
INSERT INTO batch_events (batch_id, occurred_at, kind, title, description, details)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListBatchEventsForBatch :many
SELECT * FROM batch_events
WHERE batch_id = $1
ORDER BY occurred_at ASC, id ASC;

-- name: GetBatchEvent :one
SELECT * FROM batch_events
WHERE id = $1 AND batch_id = $2;

-- name: UpdateBatchEvent :one
UPDATE batch_events SET
  occurred_at = COALESCE(sqlc.narg('occurred_at'), occurred_at),
  kind        = COALESCE(sqlc.narg('kind'),        kind),
  title       = COALESCE(sqlc.narg('title'),       title),
  description = COALESCE(sqlc.narg('description'), description),
  details     = COALESCE(sqlc.narg('details'),     details)
WHERE id = sqlc.arg('id') AND batch_id = sqlc.arg('batch_id')
RETURNING *;

-- name: DeleteBatchEvent :execrows
DELETE FROM batch_events
WHERE id = $1 AND batch_id = $2;
