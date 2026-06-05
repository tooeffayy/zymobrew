-- name: CreateBatchEvent :one
INSERT INTO batch_events (batch_id, occurred_at, kind, title, description, details)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListBatchEventsForBatch :many
-- Keyset pagination, ascending (chronological) so the journal reads
-- oldest-first. Comparison is `>` (next page = rows after the cursor row);
-- occurred_at is NOT NULL so the sort key needs no COALESCE guard — only
-- the cursor params are nullable (NULL = first page).
SELECT * FROM batch_events
WHERE batch_id = sqlc.arg('batch_id')
  AND (
    sqlc.narg('cursor_ts')::timestamptz IS NULL
    OR (occurred_at, id) > (sqlc.narg('cursor_ts')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY occurred_at ASC, id ASC
LIMIT sqlc.arg('limit_n');

-- name: ListAllBatchEventsForBatch :many
-- Unbounded chronological list for the user-export job, which must capture
-- every event regardless of page size. Not exposed over HTTP — the API uses
-- the paginated ListBatchEventsForBatch.
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
