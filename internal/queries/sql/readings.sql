-- name: CreateReading :one
INSERT INTO readings (batch_id, taken_at, gravity, temperature_c, ph, notes, source)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListReadingsForBatch :many
SELECT * FROM readings
WHERE batch_id = $1
ORDER BY taken_at ASC, id ASC;
