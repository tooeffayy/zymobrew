-- name: CreateEmailChangeRequest :one
INSERT INTO email_change_requests (
    user_id, new_email, old_email, confirm_token_hash, cancel_token_hash, expires_at
) VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- GetEmailChangeRequestByConfirmHash returns a still-valid (unexpired)
-- pending request matching the confirm-token hash. Expired rows are treated
-- as absent so a stale link can't be replayed.
-- name: GetEmailChangeRequestByConfirmHash :one
SELECT * FROM email_change_requests
WHERE confirm_token_hash = $1 AND expires_at > now();

-- GetEmailChangeRequestByCancelHash returns a still-valid (unexpired) pending
-- request matching the cancel-token hash.
-- name: GetEmailChangeRequestByCancelHash :one
SELECT * FROM email_change_requests
WHERE cancel_token_hash = $1 AND expires_at > now();

-- name: DeleteEmailChangeRequest :exec
DELETE FROM email_change_requests WHERE id = $1;

-- DeleteEmailChangeRequestsForUser clears any pending requests for a user.
-- Called before inserting a fresh request (one active change at a time) and
-- as part of account anonymization.
-- name: DeleteEmailChangeRequestsForUser :exec
DELETE FROM email_change_requests WHERE user_id = $1;

-- name: DeleteExpiredEmailChangeRequests :exec
DELETE FROM email_change_requests WHERE expires_at < now();
