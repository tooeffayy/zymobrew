-- name: CreateTastingNote :one
INSERT INTO tasting_notes (batch_id, author_id, tasted_at, rating, aroma, flavor, mouthfeel, finish, notes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: ListTastingNotesForBatch :many
SELECT * FROM tasting_notes
WHERE batch_id = $1
ORDER BY tasted_at DESC, id ASC;
