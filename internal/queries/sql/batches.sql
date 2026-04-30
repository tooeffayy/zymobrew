-- name: CreateBatch :one
INSERT INTO batches (brewer_id, recipe_id, recipe_revision_id, name, brew_type, stage, started_at, notes, visibility)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: GetBatchForUser :one
SELECT * FROM batches
WHERE id = $1 AND brewer_id = $2;

-- name: ListBatchesForUser :many
-- Sort key is COALESCE(started_at, created_at) — pre-pitch batches sort
-- by when the planning row was created, post-pitch by when fermentation
-- actually began. The cursor stores the COALESCE result, not the raw
-- columns, so the same expression appears on both sides.
SELECT * FROM batches
WHERE brewer_id = sqlc.arg('brewer_id')
  AND (
    sqlc.narg('cursor_ts')::timestamptz IS NULL
    OR (COALESCE(started_at, created_at), id) < (sqlc.narg('cursor_ts')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY COALESCE(started_at, created_at) DESC, id DESC
LIMIT sqlc.arg('limit_n');

-- name: UpdateBatch :one
UPDATE batches SET
  name       = COALESCE(sqlc.narg('name'),       name),
  stage      = COALESCE(sqlc.narg('stage'),      stage),
  notes      = COALESCE(sqlc.narg('notes'),      notes),
  started_at = COALESCE(sqlc.narg('started_at'), started_at),
  bottled_at = COALESCE(sqlc.narg('bottled_at'), bottled_at),
  visibility = COALESCE(sqlc.narg('visibility'), visibility),
  updated_at = now()
WHERE id = sqlc.arg('id') AND brewer_id = sqlc.arg('brewer_id')
RETURNING *;

-- name: DeleteBatch :execrows
DELETE FROM batches WHERE id = $1 AND brewer_id = $2;

-- name: ListAllBatchesForUser :many
SELECT * FROM batches WHERE brewer_id = $1 ORDER BY created_at ASC;
