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
-- Returns recipe ingredients joined with every inventory row that matches
-- on kind + case-insensitive name. Unit equality is intentionally NOT in
-- the join — the Go handler converts compatible-unit rows to the recipe's
-- unit (calc.Convert) and aggregates them, so a brewer with 800g + 200g
-- of the same honey reads as 1kg even when those land in two rows.
--
-- The result has multiple rows per ingredient when inventory carries the
-- ingredient in several units; ingredients with no matches still appear
-- (LEFT JOIN, NULL inventory cols).
SELECT
  ri.id            AS ingredient_id,
  ri.kind          AS ingredient_kind,
  ri.name          AS ingredient_name,
  ri.amount        AS ingredient_amount,
  ri.unit          AS ingredient_unit,
  ri.sort_order    AS ingredient_sort_order,
  inv.id           AS inventory_id,
  inv.amount       AS inventory_amount,
  inv.unit         AS inventory_unit
FROM recipe_ingredients ri
LEFT JOIN inventory_items inv
  ON inv.user_id = sqlc.arg('user_id')
 AND inv.kind = ri.kind
 AND lower(inv.name) = lower(ri.name)
WHERE ri.recipe_id = sqlc.arg('recipe_id')
ORDER BY ri.sort_order ASC, ri.id ASC, inv.created_at ASC;
