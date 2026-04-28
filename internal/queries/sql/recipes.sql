-- name: CreateRecipeDraft :one
INSERT INTO recipes (author_id, brew_type, name, style, description, target_og, target_fg, target_abv, batch_size_l, visibility)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: SetRecipeRevision :one
UPDATE recipes
SET current_revision_id = $2,
    revision_count      = revision_count + 1,
    updated_at          = now()
WHERE id = $1
RETURNING *;

-- name: UpdateRecipeMeta :one
UPDATE recipes SET
  name         = COALESCE(sqlc.narg('name'),         name),
  style        = COALESCE(sqlc.narg('style'),        style),
  description  = COALESCE(sqlc.narg('description'),  description),
  target_og    = COALESCE(sqlc.narg('target_og'),    target_og),
  target_fg    = COALESCE(sqlc.narg('target_fg'),    target_fg),
  target_abv   = COALESCE(sqlc.narg('target_abv'),   target_abv),
  batch_size_l = COALESCE(sqlc.narg('batch_size_l'), batch_size_l),
  visibility   = COALESCE(sqlc.narg('visibility'),   visibility),
  updated_at   = now()
WHERE id = sqlc.arg('id') AND author_id = sqlc.arg('author_id')
RETURNING *;

-- name: DeleteRecipe :execrows
DELETE FROM recipes WHERE id = $1 AND author_id = $2;

-- name: GetRecipeByID :one
SELECT * FROM recipes WHERE id = $1;

-- name: ListPublicRecipes :many
SELECT * FROM recipes
WHERE visibility = 'public'
ORDER BY updated_at DESC
LIMIT $1 OFFSET $2;

-- name: ListRecipesForAuthor :many
SELECT * FROM recipes
WHERE author_id = $1
ORDER BY updated_at DESC
LIMIT $2 OFFSET $3;

-- name: ListAllRecipesForAuthor :many
SELECT * FROM recipes WHERE author_id = $1 ORDER BY created_at ASC;

-- name: CreateRecipeIngredient :one
INSERT INTO recipe_ingredients (recipe_id, kind, name, amount, unit, sort_order, details)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: DeleteRecipeIngredients :exec
DELETE FROM recipe_ingredients WHERE recipe_id = $1;

-- name: ListRecipeIngredients :many
SELECT * FROM recipe_ingredients
WHERE recipe_id = $1
ORDER BY sort_order ASC, id ASC;

-- name: CreateRecipeRevision :one
INSERT INTO recipe_revisions (recipe_id, revision_number, author_id, message, name, style, description, target_og, target_fg, target_abv, batch_size_l, ingredients)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: GetRevisionByID :one
SELECT * FROM recipe_revisions WHERE id = $1;

-- name: GetRecipeRevisionByNumber :one
SELECT * FROM recipe_revisions
WHERE recipe_id = $1 AND revision_number = $2;

-- name: ListRecipeRevisions :many
SELECT * FROM recipe_revisions
WHERE recipe_id = $1
ORDER BY revision_number DESC;

-- name: CreateForkedRecipe :one
INSERT INTO recipes (author_id, parent_id, parent_revision_id, brew_type, name, style, description, target_og, target_fg, target_abv, batch_size_l, visibility)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
RETURNING *;

-- name: IncrementForkCount :exec
UPDATE recipes SET fork_count = fork_count + 1, updated_at = now() WHERE id = $1;

-- name: CreateRecipeComment :one
INSERT INTO recipe_comments (recipe_id, author_id, body)
VALUES ($1, $2, $3)
RETURNING *;

-- name: ListRecipeComments :many
SELECT
  rc.id, rc.recipe_id, rc.author_id, rc.body, rc.created_at,
  u.username AS author_username
FROM recipe_comments rc
JOIN users u ON u.id = rc.author_id
WHERE rc.recipe_id = $1
ORDER BY rc.created_at ASC
LIMIT $2 OFFSET $3;

-- name: DeleteRecipeComment :execrows
DELETE FROM recipe_comments WHERE id = $1 AND author_id = $2;

-- name: LikeRecipe :exec
INSERT INTO recipe_likes (user_id, recipe_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: UnlikeRecipe :exec
DELETE FROM recipe_likes WHERE user_id = $1 AND recipe_id = $2;
