-- name: UpsertPushDevice :one
INSERT INTO push_devices (user_id, platform, token, p256dh, auth, last_seen_at)
VALUES ($1, $2, $3, $4, $5, now())
ON CONFLICT (user_id, token) DO UPDATE SET
  last_seen_at = now(),
  p256dh       = EXCLUDED.p256dh,
  auth         = EXCLUDED.auth
RETURNING *;

-- name: DeletePushDevice :execrows
DELETE FROM push_devices WHERE user_id = $1 AND token = $2;

-- name: ListPushDevicesForUser :many
SELECT * FROM push_devices WHERE user_id = $1;

-- name: DeletePushDevicesForUser :exec
DELETE FROM push_devices WHERE user_id = $1;
