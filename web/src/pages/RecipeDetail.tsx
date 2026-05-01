import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { ApiError, Ingredient, IngredientKind, Recipe, api } from "../api";
import { useAuth } from "../auth";

// Renders one recipe by id. Public + unlisted are visible to anyone;
// private returns 404 to non-owners (server enforces this — we just
// surface whatever the API tells us).
export function RecipeDetail() {
  const { id = "" } = useParams<{ id: string }>();
  const { state } = useAuth();
  const navigate = useNavigate();
  const [recipe, setRecipe] = useState<Recipe | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<null | "delete" | "fork">(null);

  useEffect(() => {
    setLoading(true);
    setError(null);
    setRecipe(null);
    api
      .get<Recipe>(`/api/recipes/${encodeURIComponent(id)}`)
      .then((r) => setRecipe(r))
      .catch((e: unknown) => {
        if (e instanceof ApiError) {
          setError({ status: e.status, message: e.message });
        } else {
          setError({ status: 0, message: "failed to load recipe" });
        }
      })
      .finally(() => setLoading(false));
  }, [id]);

  if (loading) {
    return (
      <div className="page">
        <p className="muted">Loading…</p>
      </div>
    );
  }

  if (error) {
    return (
      <div className="page">
        <p className="back-link"><Link to="/">← Back to recipes</Link></p>
        <h1>{error.status === 404 ? "Recipe not found" : "Couldn't load recipe"}</h1>
        {error.status !== 404 && <p className="error">{error.message}</p>}
        <p className="muted">
          {error.status === 404
            ? "It may have been deleted, or it's private."
            : "Try again in a moment."}
        </p>
      </div>
    );
  }

  if (!recipe) return null;

  const isAuthed = state.status === "authed";
  const isOwner = isAuthed && state.user.id === recipe.author_id;

  const onDelete = async () => {
    // Native confirm keeps the destructive-action surface small and
    // matches the no-framework styling pass. Swap for a custom dialog
    // when there's a second destructive action that needs the same
    // affordance.
    if (!window.confirm(`Delete "${recipe.name}"? This cannot be undone.`)) {
      return;
    }
    setActionError(null);
    setBusy("delete");
    try {
      await api.delete(`/api/recipes/${encodeURIComponent(recipe.id)}`);
      navigate("/", { replace: true });
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "delete failed");
      setBusy(null);
    }
  };

  const onFork = async () => {
    setActionError(null);
    setBusy("fork");
    try {
      const fork = await api.post<Recipe>(
        `/api/recipes/${encodeURIComponent(recipe.id)}/fork`,
        {},
      );
      navigate(`/recipes/${fork.id}`);
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "fork failed");
      setBusy(null);
    }
  };

  return (
    <div className="page">
      <p className="back-link"><Link to="/">← Back to recipes</Link></p>

      <header className="recipe-header">
        <div className="recipe-header-meta">
          <span className={`pill pill-${recipe.brew_type}`}>{recipe.brew_type}</span>
          {recipe.style && <span className="muted">{recipe.style}</span>}
          {recipe.visibility !== "public" && (
            <span className="pill pill-visibility">{recipe.visibility}</span>
          )}
          {isOwner && <span className="pill pill-owner">Yours</span>}
        </div>
        <h1>{recipe.name}</h1>
        {recipe.description && <p className="recipe-detail-desc">{recipe.description}</p>}
      </header>

      {isAuthed && (
        <div className="recipe-actions">
          <Link
            to={`/batches/new?recipe=${encodeURIComponent(recipe.id)}`}
            className="action-button action-primary"
          >
            Brew this
          </Link>
          {isOwner && (
            <Link to={`/recipes/${recipe.id}/edit`} className="action-button">
              Edit
            </Link>
          )}
          <button
            type="button"
            className="action-button"
            onClick={onFork}
            disabled={busy !== null}
          >
            {busy === "fork" ? "Forking…" : "Fork"}
          </button>
          {isOwner && (
            <button
              type="button"
              className="action-button danger"
              onClick={onDelete}
              disabled={busy !== null}
            >
              {busy === "delete" ? "Deleting…" : "Delete"}
            </button>
          )}
        </div>
      )}

      {actionError && <p className="error">{actionError}</p>}

      <section className="stats-grid">
        <Stat label="OG" value={fmtGravity(recipe.target_og)} />
        <Stat label="FG" value={fmtGravity(recipe.target_fg)} />
        <Stat label="ABV" value={recipe.target_abv != null ? `${recipe.target_abv.toFixed(1)}%` : null} />
        <Stat label="Batch" value={recipe.batch_size_l != null ? `${recipe.batch_size_l} L` : null} />
      </section>

      <section className="recipe-section">
        <h2>Ingredients</h2>
        {recipe.ingredients.length === 0 ? (
          <p className="muted">No ingredients listed.</p>
        ) : (
          <IngredientList ingredients={recipe.ingredients} />
        )}
      </section>

      <footer className="recipe-footer muted">
        Revision {recipe.revision_number} of {recipe.revision_count}
        {" · "}
        {recipe.fork_count} {recipe.fork_count === 1 ? "fork" : "forks"}
        {" · "}
        Updated {fmtDate(recipe.updated_at)}
      </footer>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string | null }) {
  return (
    <div className="stat">
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value ?? <span className="muted">—</span>}</div>
    </div>
  );
}

// Group by kind and render — order kinds by typical recipe-listing
// convention (fermentables first, then yeast, then adjuncts).
const KIND_ORDER: IngredientKind[] = [
  "honey", "juice", "sugar", "fruit",
  "yeast", "nutrient",
  "water", "acid", "tannin", "spice", "oak", "other",
];

function IngredientList({ ingredients }: { ingredients: Ingredient[] }) {
  const groups = new Map<IngredientKind, Ingredient[]>();
  for (const ing of ingredients) {
    const arr = groups.get(ing.kind) ?? [];
    arr.push(ing);
    groups.set(ing.kind, arr);
  }
  for (const arr of groups.values()) {
    arr.sort((a, b) => a.sort_order - b.sort_order);
  }
  const ordered = KIND_ORDER.filter((k) => groups.has(k)).map((k) => [k, groups.get(k)!] as const);

  return (
    <div className="ingredient-card">
      {ordered.map(([kind, items]) => (
        <section key={kind} className="ingredient-section">
          <h3 className="ingredient-kind">{kind}</h3>
          {items.map((ing) => (
            <div key={ing.id} className="ingredient-row">
              <span className="ingredient-name">{ing.name}</span>
              {ing.amount != null && (
                <span className="ingredient-amount">
                  {ing.amount}
                  {ing.unit ? ` ${ing.unit}` : ""}
                </span>
              )}
            </div>
          ))}
        </section>
      ))}
    </div>
  );
}

// Gravities print to 3 decimals (1.085) — that's the ubiquitous brewing
// notation; trimming trailing zeros would just confuse readers.
function fmtGravity(g: number | undefined): string | null {
  if (g == null) return null;
  return g.toFixed(3);
}

function fmtDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    });
  } catch {
    return iso;
  }
}
