-- name: CreateUser :one
INSERT INTO users (username, email, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByUsername :one
SELECT * FROM users
WHERE username = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT * FROM users
WHERE email = $1 AND deleted_at IS NULL;

-- name: ListUsers :many
SELECT * FROM users
WHERE deleted_at IS NULL
ORDER BY created_at DESC
LIMIT $1 OFFSET $2;

-- name: CountUsers :one
SELECT count(*) FROM users
WHERE deleted_at IS NULL;
