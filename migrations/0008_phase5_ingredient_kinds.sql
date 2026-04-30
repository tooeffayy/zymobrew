-- +goose Up

-- Phase 5 (cider + wine) introduces two ingredient categories that were
-- previously bucketed under 'other'. ADD VALUE IF NOT EXISTS is idempotent
-- so re-running the migration on a partially-applied DB is harmless.
--
-- 'juice'  — apple/grape juice + concentrate (cider/wine base).
-- 'sugar'  — chaptalization sugars (cane, dextrose, etc.).

ALTER TYPE ingredient_kind ADD VALUE IF NOT EXISTS 'juice';
ALTER TYPE ingredient_kind ADD VALUE IF NOT EXISTS 'sugar';

-- +goose Down

-- Postgres does not support removing values from an enum type without
-- dropping and recreating it (and rewriting every row referencing the type).
-- Down is a no-op; rolling back this migration is not supported.
SELECT 1;
