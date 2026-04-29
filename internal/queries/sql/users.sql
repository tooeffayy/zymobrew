-- name: CreateUser :one
INSERT INTO users (username, email, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateUserWithPassword :one
INSERT INTO users (username, email, display_name, password_hash, is_admin)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserCredentialByUsername :one
SELECT id, username, email, password_hash FROM users
WHERE username = $1 AND deleted_at IS NULL;

-- name: GetUserCredentialByEmail :one
SELECT id, username, email, password_hash FROM users
WHERE email = $1 AND deleted_at IS NULL;

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

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = $1
WHERE id = $2 AND deleted_at IS NULL;

-- name: UpdateUser :one
UPDATE users SET
  display_name = COALESCE(sqlc.narg('display_name'), display_name),
  bio          = COALESCE(sqlc.narg('bio'),          bio),
  avatar_url   = COALESCE(sqlc.narg('avatar_url'),   avatar_url)
WHERE id = sqlc.arg('id') AND deleted_at IS NULL
RETURNING *;

-- AnonymizeUser strips PII from a user row in place. The row is preserved so
-- foreign keys on immutable history (recipe_revisions, admin_audit_log) and
-- public content (recipes, comments) remain valid. Username/email are
-- replaced with derived placeholders that satisfy the UNIQUE constraints;
-- the .invalid TLD is reserved (RFC 2606) so the address can never resolve.
-- name: AnonymizeUser :exec
UPDATE users SET
  username               = 'deleted-' || id::text,
  email                  = 'deleted-' || id::text || '@deleted.invalid',
  password_hash          = NULL,
  display_name           = NULL,
  bio                    = NULL,
  avatar_url             = NULL,
  deletion_scheduled_for = NULL,
  deletion_choices       = NULL,
  deletion_reason        = NULL,
  deleted_at             = now()
WHERE id = $1;

-- name: CreateAccountDeletionRequest :one
INSERT INTO account_deletion_requests (user_id) VALUES ($1) RETURNING *;

-- ListUnprocessedDeletionRequests returns deletion requests for users whose
-- anonymization was undone by a backup restore. Used by the
-- `zymo reprocess-deletions` command to re-apply pending deletions.
-- name: ListUnprocessedDeletionRequests :many
SELECT adr.id, adr.user_id, adr.requested_at
FROM account_deletion_requests adr
JOIN users u ON u.id = adr.user_id
WHERE u.deleted_at IS NULL
ORDER BY adr.requested_at;
