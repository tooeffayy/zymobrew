-- +goose Up

-- Per-user ingredient stockpile. Mirrors the recipe_ingredients shape
-- ((kind, name, amount, unit)) so the recipe-match join can compare
-- like-to-like without a translation layer. v1 matches strictly on
-- (kind, lower(name), unit); a normalized catalog and unit conversion
-- are deferred until usage shows the simple match isn't enough.
--
-- amount is nullable to allow "I have some, quantity unknown" entries —
-- the match logic treats NULL as "have it, can't measure shortfall".
CREATE TABLE inventory_items (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        ingredient_kind NOT NULL,
  name        TEXT NOT NULL,
  amount      NUMERIC(10,3),
  unit        TEXT,
  notes       TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-user list view; recipe-match join hits (user_id, kind) first then
-- filters by name. A second functional index on lower(name) speeds the
-- match's case-insensitive comparison without forcing every read to pay
-- for it.
CREATE INDEX inventory_items_user_idx ON inventory_items(user_id, kind);
CREATE INDEX inventory_items_user_name_idx ON inventory_items(user_id, kind, lower(name));

-- +goose Down

DROP INDEX IF EXISTS inventory_items_user_name_idx;
DROP INDEX IF EXISTS inventory_items_user_idx;
DROP TABLE IF EXISTS inventory_items;
