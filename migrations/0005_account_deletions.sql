-- +goose Up

-- Audit trail for account-deletion (anonymization) requests. Survives the
-- anonymization itself so that if an admin restores a pre-deletion backup,
-- a startup hook (or `zymo reprocess-deletions`) can find users whose
-- deletion was undone by the restore and re-run the anonymize transaction.
CREATE TABLE account_deletion_requests (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX account_deletion_requests_user_idx ON account_deletion_requests(user_id);

-- +goose Down

DROP TABLE IF EXISTS account_deletion_requests;
