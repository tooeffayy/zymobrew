-- name: GetUserPrefs :one
SELECT * FROM user_prefs WHERE user_id = $1;

-- Upsert with partial overwrite: NULL values fall back to defaults on
-- INSERT and to the existing value on UPDATE (COALESCE pattern matching
-- the rest of the PATCH endpoints).
-- name: UpsertUserPrefs :one
INSERT INTO user_prefs (user_id, degree_units, timezone)
VALUES (
  sqlc.arg('user_id'),
  COALESCE(sqlc.narg('degree_units'), 'C'),
  COALESCE(sqlc.narg('timezone'), 'UTC')
)
ON CONFLICT (user_id) DO UPDATE SET
  degree_units = COALESCE(sqlc.narg('degree_units'), user_prefs.degree_units),
  timezone     = COALESCE(sqlc.narg('timezone'),     user_prefs.timezone)
RETURNING *;

-- name: DeleteUserPrefsForUser :exec
DELETE FROM user_prefs WHERE user_id = $1;
