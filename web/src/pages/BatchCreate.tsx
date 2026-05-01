import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";

import { ApiError, Batch, Recipe, api } from "../api";
import { BatchForm } from "../components/BatchForm";

// /batches/new — supports `?recipe=<id>` to pre-pin a recipe (the
// "Brew this" entry point on RecipeDetail). When pinned, brew_type is
// locked to the recipe's type and the batch's `recipe_id` is sent on
// create so the server materializes batch_start reminders if a start
// time is set.
export function BatchCreate() {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const recipeID = params.get("recipe");

  const [pinned, setPinned] = useState<Recipe | null>(null);
  const [pinnedError, setPinnedError] = useState<string | null>(null);
  const [pinnedLoading, setPinnedLoading] = useState<boolean>(recipeID !== null);

  useEffect(() => {
    if (!recipeID) return;
    setPinnedLoading(true);
    setPinnedError(null);
    api
      .get<Recipe>(`/api/recipes/${encodeURIComponent(recipeID)}`)
      .then((r) => setPinned(r))
      .catch((e: unknown) => {
        // 404 here means a non-owner clicked a stale "brew this" link
        // for a recipe that's been deleted/made private. We fall
        // through to the freeform form rather than block — the user
        // can still create an unpinned batch.
        const msg = e instanceof ApiError ? e.message : "failed to load recipe";
        setPinnedError(msg);
      })
      .finally(() => setPinnedLoading(false));
  }, [recipeID]);

  if (pinnedLoading) {
    return (
      <div className="page">
        <p className="muted">Loading…</p>
      </div>
    );
  }

  return (
    <div className="page">
      <p className="back-link"><Link to="/batches">← Back to batches</Link></p>
      <h1>Start a batch</h1>

      {pinnedError && (
        <p className="error">Couldn't load that recipe: {pinnedError}</p>
      )}

      <BatchForm
        mode="create"
        pinnedRecipe={pinned ?? undefined}
        submitLabel="Start batch"
        submittingLabel="Starting…"
        cancelTo={pinned ? `/recipes/${pinned.id}` : "/batches"}
        onSubmit={async (payload) => {
          const res = await api.post<Batch>("/api/batches", payload);
          navigate(`/batches/${res.id}`, { replace: true });
        }}
      />
    </div>
  );
}
