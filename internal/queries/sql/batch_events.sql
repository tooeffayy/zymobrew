-- name: CreateBatchEvent :one
INSERT INTO batch_events (batch_id, occurred_at, kind, title, description, details)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListBatchEventsForBatch :many
SELECT * FROM batch_events
WHERE batch_id = $1
ORDER BY occurred_at ASC, id ASC;
