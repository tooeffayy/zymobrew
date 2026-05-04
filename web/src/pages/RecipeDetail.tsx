import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  Cell,
  Column,
  Row,
  Table,
  TableBody,
  TableHeader,
} from "react-aria-components";

import {
  ApiError,
  Ingredient,
  IngredientKind,
  InventoryMatch,
  InventoryMatchResponse,
  Recipe,
  api,
} from "../api";
import { useAuth } from "../auth";
import { ReminderTemplatesSection } from "../components/ReminderTemplatesSection";

// Renders one recipe by id. Public + unlisted are visible to anyone;
// private returns 404 to non-owners (server enforces this — we just
// surface whatever the API tells us).
export function RecipeDetail() {
  const { id = "" } = useParams<{ id: string }>();
  const { state } = useAuth();
  const navigate = useNavigate();
  const [recipe, setRecipe] = useState<Recipe | null>(null);
  const [match, setMatch] = useState<InventoryMatch[] | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<null | "delete" | "fork">(null);

  const isAuthed = state.status === "authed";

  useEffect(() => {
    setLoading(true);
    setError(null);
    setRecipe(null);
    setMatch(null);
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

  // Fetch inventory match in parallel — runs only for authed users
  // (anon callers get a 401 from the endpoint). A failure here is
  // non-fatal: the badges just don't render. We don't surface the
  // error because the recipe itself loaded fine and inventory is
  // a secondary signal.
  useEffect(() => {
    if (!id || !isAuthed) {
      setMatch(null);
      return;
    }
    let cancelled = false;
    api
      .get<InventoryMatchResponse>(`/api/recipes/${encodeURIComponent(id)}/inventory-match`)
      .then((res) => {
        if (!cancelled) setMatch(res.items);
      })
      .catch(() => {
        if (!cancelled) setMatch(null);
      });
    return () => { cancelled = true; };
  }, [id, isAuthed]);

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

  const isOwner = state.status === "authed" && state.user.id === recipe.author_id;

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
          <>
            {match && match.length > 0 && <InventoryMatchSummary match={match} />}
            <IngredientList ingredients={recipe.ingredients} match={match} />
          </>
        )}
      </section>

      <ReminderTemplatesSection recipeID={recipe.id} isOwner={isOwner} />

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

function IngredientList({
  ingredients, match,
}: {
  ingredients: Ingredient[];
  match: InventoryMatch[] | null;
}) {
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

  // Index match rows by ingredient_id for O(1) lookup. The server returns
  // exactly one row per recipe ingredient, so a missing entry means we
  // never fetched the match (anon caller, fetch failed); render the row
  // un-decorated in that case.
  const matchByID = new Map<string, InventoryMatch>();
  if (match) {
    for (const m of match) matchByID.set(m.ingredient_id, m);
  }

  // One Table per kind so the kind heading stays as a section break.
  // Every table uses the same fixed column widths (table-layout: fixed
  // + explicit widths in CSS), so qty/unit columns line up vertically
  // across all sections — that's the alignment we couldn't get from a
  // flex row with a packed "amount unit" string.
  return (
    <div className="ingredient-card">
      {ordered.map(([kind, items]) => (
        <section key={kind} className="ingredient-section">
          <h3 className="ingredient-kind">{kind}</h3>
          <Table aria-label={`${kind} ingredients`} className="ingredient-table">
            <TableHeader>
              <Column isRowHeader className="ingredient-col-name">Name</Column>
              <Column className="ingredient-col-qty">Quantity</Column>
              <Column className="ingredient-col-unit">Unit</Column>
            </TableHeader>
            <TableBody items={items}>
              {(ing) => {
                const m = matchByID.get(ing.id);
                return (
                  <Row className="ingredient-row">
                    <Cell className="ingredient-name">
                      <span className="ingredient-name-text">{ing.name}</span>
                      {m && <InventoryBadge match={m} />}
                    </Cell>
                    <Cell className="ingredient-qty">
                      {ing.amount != null ? ing.amount : ""}
                    </Cell>
                    <Cell className="ingredient-unit">
                      {ing.amount != null && ing.unit ? ing.unit : ""}
                    </Cell>
                  </Row>
                );
              }}
            </TableBody>
          </Table>
        </section>
      ))}
    </div>
  );
}

// Per-ingredient have/short/missing pill. The shortfall quantity is
// inlined for the short case so the brewer reads the gap without
// having to compare two amounts. unit_mismatch surfaces as a hint
// suffix on the missing case — strict matching can't bridge units,
// but knowing you have the right kind in a different unit is useful.
function InventoryBadge({ match }: { match: InventoryMatch }) {
  if (match.status === "have") {
    return (
      <span className="inventory-badge inventory-badge-have" title="In your inventory">
        have
      </span>
    );
  }
  if (match.status === "short") {
    const gap = match.shortfall != null
      ? `short ${fmtBadgeAmount(match.shortfall)}${match.unit ? ` ${match.unit}` : ""}`
      : "short";
    return (
      <span
        className="inventory-badge inventory-badge-short"
        title={
          match.inventory_amount != null
            ? `You have ${match.inventory_amount}${match.unit ? ` ${match.unit}` : ""}`
            : undefined
        }
      >
        {gap}
      </span>
    );
  }
  if (match.unit_mismatch) {
    return (
      <span
        className="inventory-badge inventory-badge-mismatch"
        title="You have this kind on hand, but only in an incompatible unit (e.g. recipe wants grams, inventory has 'sticks'). Convert by hand or add a row in a compatible unit."
      >
        unit mismatch
      </span>
    );
  }
  return (
    <span className="inventory-badge inventory-badge-missing" title="Not in your inventory">
      missing
    </span>
  );
}

// Above-the-list summary: "3 of 5 in stock — 2 missing, 1 short" or
// the all-clear "Everything in stock". Hides itself when the recipe
// has no ingredients (caller-guarded) or when the match is empty.
function InventoryMatchSummary({ match }: { match: InventoryMatch[] }) {
  let have = 0, short = 0, missing = 0;
  for (const m of match) {
    if (m.status === "have") have++;
    else if (m.status === "short") short++;
    else missing++;
  }
  const total = match.length;
  if (have === total) {
    return (
      <p className="inventory-summary inventory-summary-ready">
        Everything in stock — ready to brew.{" "}
        <Link to="/inventory" className="inventory-summary-link">Manage inventory</Link>
      </p>
    );
  }
  const parts: string[] = [];
  if (missing > 0) parts.push(`${missing} missing`);
  if (short > 0)   parts.push(`${short} short`);
  return (
    <p className="inventory-summary">
      <strong>{have} of {total}</strong> in stock — {parts.join(", ")}.{" "}
      <Link to="/inventory" className="inventory-summary-link">Manage inventory</Link>
    </p>
  );
}

function fmtBadgeAmount(n: number): string {
  return n.toFixed(3).replace(/\.?0+$/, "");
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
