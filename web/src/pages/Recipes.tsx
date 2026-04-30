import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { ApiError, api, RecipeListItem, RecipePage } from "../api";
import { useAuth } from "../auth";

// Public recipe feed. GET /api/recipes is anonymous-safe (no security
// requirement in the OpenAPI), so this page renders for both authed and
// anon visitors. Pagination is opaque cursor + Load More button — we
// avoid infinite scroll until we have a real reason to wire it up.
export function Recipes() {
  const { state } = useAuth();
  const [recipes, setRecipes] = useState<RecipeListItem[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  // `done` distinguishes "first page hasn't loaded" from "we've reached
  // the end" — without it the empty state and the end-of-list state are
  // indistinguishable.
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadPage = async (after: string | null) => {
    setLoading(true);
    setError(null);
    try {
      const qs = after ? `?cursor=${encodeURIComponent(after)}` : "";
      const page = await api.get<RecipePage>(`/api/recipes${qs}`);
      setRecipes((prev) => (after ? [...prev, ...page.recipes] : page.recipes));
      setCursor(page.next_cursor);
      setDone(page.next_cursor === null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "failed to load recipes");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadPage(null);
  }, []);

  return (
    <div className="page">
      <h1>Recipes</h1>
      {error && <p className="error">{error}</p>}
      {recipes.length === 0 && done && !error && (
        <div className="empty-state">
          <p className="muted">No public recipes yet.</p>
          {state.status === "authed" && (
            <Link to="/recipes/new" className="empty-cta">+ Create the first one</Link>
          )}
        </div>
      )}
      <ul className="recipe-list">
        {recipes.map((r) => (
          <li key={r.id} className="recipe-card">
            <Link to={`/recipes/${r.id}`} className="recipe-title">{r.name}</Link>
            <div className="recipe-meta">
              <span className={`pill pill-${r.brew_type}`}>{r.brew_type}</span>
              {r.style && <span className="muted">{r.style}</span>}
            </div>
            {r.description && <p className="recipe-desc">{r.description}</p>}
            <div className="recipe-stats muted">
              {r.target_og && <span>OG {r.target_og.toFixed(3)}</span>}
              {r.target_fg && <span>FG {r.target_fg.toFixed(3)}</span>}
              {r.target_abv && <span>{r.target_abv.toFixed(1)}% ABV</span>}
              {r.batch_size_l && <span>{r.batch_size_l} L</span>}
              <span>{r.fork_count} forks</span>
            </div>
          </li>
        ))}
      </ul>
      {!done && recipes.length > 0 && (
        <button type="button" disabled={loading} onClick={() => loadPage(cursor)}>
          {loading ? "Loading…" : "Load more"}
        </button>
      )}
      {loading && recipes.length === 0 && <p className="muted">Loading…</p>}
    </div>
  );
}
