-- name: CreateReading :one
INSERT INTO readings (batch_id, taken_at, gravity, temperature_c, ph, notes, source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListReadingsForBatch :many
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
