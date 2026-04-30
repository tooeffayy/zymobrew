import { useEffect, useState } from "react";
import { Link, Navigate, useNavigate, useParams } from "react-router-dom";

import { ApiError, Recipe, api } from "../api";
import { useAuth } from "../auth";
import { RecipeForm } from "../components/RecipeForm";

// /recipes/:id/edit — owner-only PATCH form. Non-owners get redirected
// home rather than a useless error screen; we let the API decide
// (visibility = 404 to non-owners, then we treat 404/403 as "not yours
// to edit" and bounce). Auth itself is enforced by RequireAuth above.
export function RecipeEdit() {
  const { id = "" } = useParams<{ id: string }>();
  const { state } = useAuth();
  const navigate = useNavigate();
  const [recipe, setRecipe] = useState<Recipe | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);

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

  if (error || !recipe) {
    return (
      <div className="page">
        <p className="back-link"><Link to="/">← Back to recipes</Link></p>
        <h1>Recipe not found</h1>
        <p className="muted">It may have been deleted, or it's not yours to edit.</p>
      </div>
    );
  }

  // Owner check is the server's job too (PATCH 404s for non-owners),
  // but bouncing here saves a wasted request on a form the user can't
  // submit. RequireAuth ensures state.status === "authed".
  if (state.status === "authed" && state.user.id !== recipe.author_id) {
    return <Navigate to={`/recipes/${recipe.id}`} replace />;
  }

  return (
    <div className="page">
      <p className="back-link">
        <Link to={`/recipes/${recipe.id}`}>← Back to recipe</Link>
      </p>
      <h1>Edit recipe</h1>

      <RecipeForm
        mode="edit"
        initial={recipe}
        submitLabel="Save changes"
        submittingLabel="Saving…"
        cancelTo={`/recipes/${recipe.id}`}
        onSubmit={async (payload) => {
          const res = await api.patch<Recipe>(`/api/recipes/${encodeURIComponent(recipe.id)}`, payload);
          navigate(`/recipes/${res.id}`, { replace: true });
        }}
      />
    </div>
  );
}
