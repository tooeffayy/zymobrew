-- name: CreateReading :one
INSERT INTO readings (batch_id, taken_at, gravity, temperature_c, ph, notes, source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListReadingsForBatch :many
-- Keyset pagination, ascending (chronological) so the chart and table get
-- readings oldest-first. Unlike the DESC list endpoints the comparison is
-- `>`: the next page is the rows *after* the cursor row. taken_at is NOT
-- NULL so no COALESCE guard is needed on the sort key — only the cursor
-- params themselves are nullable (NULL = first page).
SELECT * FROM readings
WHERE batch_id = sqlc.arg('batch_id')
  AND (
    sqlc.narg('cursor_ts')::timestamptz IS NULL
    OR (taken_at, id) > (sqlc.narg('cursor_ts')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY taken_at ASC, id ASC
LIMIT sqlc.arg('limit_n');

-- name: ListAllReadingsForBatch :many
-- Unbounded chronological list for the user-export job, which must capture
-- every reading regardless of page size. Not exposed over HTTP — the API
-- uses the paginated ListReadingsForBatch.
SELECT * FROM readings
WHERE batch_id = $1
ORDER BY taken_at ASC, id ASC;

-- name: UpdateReading :one
UPDATE readings SET
  taken_at      = COALESCE(sqlc.narg('taken_at'),      taken_at),
  gravity       = COALESCE(sqlc.narg('gravity'),       gravity),
  temperature_c = COALESCE(sqlc.narg('temperature_c'), temperature_c),
  ph            = COALESCE(sqlc.narg('ph'),            ph),
  notes         = COALESCE(sqlc.narg('notes'),         notes)
WHERE id = sqlc.arg('id') AND batch_id = sqlc.arg('batch_id')
RETURNING *;

-- name: DeleteReading :execrows
DELETE FROM readings
WHERE id = $1 AND batch_id = $2;

-- name: DeleteReadingsBulk :execrows
DELETE FROM readings
WHERE batch_id = sqlc.arg('batch_id') AND id = ANY(sqlc.arg('ids')::uuid[]);
