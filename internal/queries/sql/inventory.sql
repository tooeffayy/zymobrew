-- name: CreateInventoryItem :one
INSERT INTO inventory_items (user_id, kind, name, amount, unit, notes)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInventoryItemForUser :one
SELECT * FROM inventory_items WHERE id = $1 AND user_id = $2;

-- name: ListInventoryForUser :many
-- Inventory is bounded (a brewer keeps tens, not thousands of items),
-- so plain ORDER BY without keyset pagination is fine for v1. Sort by
-- kind first then name so the page reads as a categorized list.
SELECT * FROM inventory_items
WHERE user_id = $1
ORDER BY kind ASC, lower(name) ASC, created_at ASC;

-- name: UpdateInventoryItem :one
-- COALESCE PATCH semantics — omitted columns retain their value. amount
-- and unit can't be cleared to NULL via PATCH (matches the rest of the
-- API). notes can be cleared via empty string.
UPDATE inventory_items SET
  kind       = COALESCE(sqlc.narg('kind'),       kind),
  name       = COALESCE(sqlc.narg('name'),       name),
  amount     = COALESCE(sqlc.narg('amount'),     amount),
  unit       = COALESCE(sqlc.narg('unit'),       unit),
  notes      = COALESCE(sqlc.narg('notes'),      notes),
  updated_at = now()
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id')
RETURNING *;

-- name: DeleteInventoryItem :execrows
DELETE FROM inventory_items WHERE id = $1 AND user_id = $2;

-- name: DeleteAllInventoryForUser :exec
-- Used by account anonymization to wipe inventory alongside other
-- per-user data. Returns nothing — caller doesn't care about the count.
DELETE FROM inventory_items WHERE user_id = $1;

-- name: MatchInventoryForRecipe :many
-- Returns one row per recipe ingredient with the matching inventory
-- item joined in (NULL inventory cols mean "missing"). Match is strict:
-- same kind, case-insensitive name, same unit (or both NULL). Unit
-- conversion is deferred — the handler reports a "unit mismatch" hint
-- separately when names match but units don't.
SELECT
  ri.id            AS ingredient_id,
  ri.kind          AS ingredient_kind,
  ri.name          AS ingredient_name,
  ri.amount        AS ingredient_amount,
  ri.unit          AS ingredient_unit,
  inv.id           AS inventory_id,
  inv.amount       AS inventory_amount,
  inv.unit         AS inventory_unit,
  -- Separately surface the "name matches but unit doesn't" case so the
  -- UI can flag a unit mismatch instead of silently calling it missing.
  EXISTS (
    SELECT 1 FROM inventory_items inv2
    WHERE inv2.user_id = sqlc.arg('user_id')
      AND inv2.kind = ri.kind
      AND lower(inv2.name) = lower(ri.name)
      AND inv2.unit IS DISTINCT FROM ri.unit
  ) AS has_unit_mismatch
FROM recipe_ingredients ri
LEFT JOIN inventory_items inv
  ON inv.user_id = sqlc.arg('user_id')
 AND inv.kind = ri.kind
 AND lower(inv.name) = lower(ri.name)
 AND inv.unit IS NOT DISTINCT FROM ri.unit
WHERE ri.recipe_id = sqlc.arg('recipe_id')
ORDER BY ri.sort_order ASC, ri.id ASC;
