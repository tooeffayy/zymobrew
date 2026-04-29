-- =============================================================================
-- User exports
-- =============================================================================

-- name: CreateUserExport :one
INSERT INTO user_exports (user_id, status) VALUES ($1, 'pending') RETURNING *;

-- name: GetUserExport :one
SELECT * FROM user_exports WHERE id = $1 AND user_id = $2;

-- name: ListUserExports :many
SELECT * FROM user_exports WHERE user_id = $1 ORDER BY created_at DESC LIMIT 20;

-- name: GetPendingUserExport :one
SELECT * FROM user_exports
WHERE user_id = $1 AND status IN ('pending', 'running')
LIMIT 1;

-- name: ClaimPendingUserExports :many
UPDATE user_exports SET status = 'running'
WHERE id IN (
  SELECT id FROM user_exports WHERE status = 'pending'
  ORDER BY created_at
  LIMIT 5
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteUserExport :one
UPDATE user_exports
SET status = 'complete', file_path = $2, size_bytes = $3, sha256 = $4,
    completed_at = now(), expires_at = now() + interval '7 days'
WHERE id = $1
RETURNING *;

-- name: FailUserExport :exec
UPDATE user_exports SET status = 'failed', error = $2, completed_at = now()
WHERE id = $1;

-- name: ExpireUserExports :many
UPDATE user_exports SET status = 'expired'
WHERE expires_at < now() AND status = 'complete'
RETURNING file_path;

-- name: ListUserExportFilePathsForUser :many
SELECT file_path FROM user_exports
WHERE user_id = $1 AND file_path IS NOT NULL;

-- name: DeleteUserExportsForUser :exec
DELETE FROM user_exports WHERE user_id = $1;

-- =============================================================================
-- Admin backups
-- =============================================================================

-- name: CreateAdminBackup :one
INSERT INTO admin_backups (status, storage_backend) VALUES ('pending', $1) RETURNING *;

-- name: GetAdminBackup :one
SELECT * FROM admin_backups WHERE id = $1;

-- name: ListAdminBackups :many
SELECT * FROM admin_backups ORDER BY created_at DESC LIMIT 20;

-- name: ClaimPendingAdminBackups :many
UPDATE admin_backups SET status = 'running'
WHERE id IN (
  SELECT id FROM admin_backups WHERE status = 'pending'
  ORDER BY created_at
  LIMIT 1
  FOR UPDATE SKIP LOCKED
)
RETURNING *;

-- name: CompleteAdminBackup :one
UPDATE admin_backups
SET status = 'complete', file_path = $2, size_bytes = $3, sha256 = $4, completed_at = now()
WHERE id = $1
RETURNING *;

-- name: FailAdminBackup :exec
UPDATE admin_backups SET status = 'failed', error = $2, completed_at = now()
WHERE id = $1;

-- name: DeleteExpiredAdminBackups :many
DELETE FROM admin_backups
WHERE completed_at < now() - ($1::int * interval '1 day')
  AND status IN ('complete', 'failed')
RETURNING file_path;

-- =============================================================================
-- Social data for export
-- =============================================================================

-- name: ListFollowsByUser :many
SELECT * FROM follows WHERE follower_id = $1 ORDER BY created_at ASC;

-- name: ListLikesByUser :many
SELECT * FROM recipe_likes WHERE user_id = $1 ORDER BY created_at ASC;

-- name: ListRecipeCommentsByUser :many
SELECT * FROM recipe_comments WHERE author_id = $1 ORDER BY created_at ASC;
