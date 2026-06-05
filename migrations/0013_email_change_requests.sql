-- +goose Up

-- Pending email-change requests. A change is initiated (after password
-- re-auth) but does NOT touch users.email until the owner clicks a
-- confirmation link mailed to the NEW address — that proves the new address
-- is real and controlled by the requester. A second token is mailed to the
-- OLD address so the legitimate owner can cancel a change they didn't start
-- (the tripwire against a hijacked session quietly swapping the email).
--
-- Two independent capability tokens per request, each stored only as a
-- SHA-256 hash (same scheme as sessions.token_hash) so a DB read can't
-- replay a link. new_email is CITEXT to match users.email's case-insensitive
-- uniqueness; the real uniqueness check happens against users at confirm
-- time, since another account could claim the address in the interim.
CREATE TABLE email_change_requests (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    new_email          CITEXT NOT NULL,
    old_email          CITEXT NOT NULL,
    confirm_token_hash TEXT NOT NULL UNIQUE,
    cancel_token_hash  TEXT NOT NULL UNIQUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at         TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_email_change_requests_user ON email_change_requests(user_id);

-- +goose Down

DROP TABLE email_change_requests;
