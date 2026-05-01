import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { ApiError, Batch, BatchPage, api } from "../api";

// /batches — owner-only list of the current user's batches. RequireAuth
// wraps the route so we never render this with state.status === "anon".
// Server enforces brewer_id scoping; we just paginate the response.
export function Batches() {
  const [batches, setBatches] = useState<Batch[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadPage = async (after: string | null) => {
    setLoading(true);
    setError(null);
    try {
      const qs = after ? `?cursor=${encodeURIComponent(after)}` : "";
      const page = await api.get<BatchPage>(`/api/batches${qs}`);
      setBatches((prev) => (after ? [...prev, ...page.batches] : page.batches));
      setCursor(page.next_cursor);
      setDone(page.next_cursor === null);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "failed to load batches");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadPage(null);
  }, []);

  return (
    <div className="page">
      <h1>Batches</h1>
      {error && <p className="error">{error}</p>}
      {batches.length === 0 && done && !error && (
        <div className="empty-state">
          <p className="muted">No batches yet — start one from a recipe, or create one freeform.</p>
          <Link to="/batches/new" className="empty-cta">+ Start a batch</Link>
        </div>
      )}
      <ul className="recipe-list">
        {batches.map((b) => (
          <li key={b.id} className="recipe-card">
            <Link to={`/batches/${b.id}`} className="recipe-title">{b.name}</Link>
            <div className="recipe-meta">
              <span className={`pill pill-${b.brew_type}`}>{b.brew_type}</span>
              <span className={`stage stage-${b.stage}`}>{b.stage}</span>
              {b.started_at && <span className="muted">Started {fmtDate(b.started_at)}</span>}
            </div>
            {b.notes && <p className="recipe-desc">{b.notes}</p>}
          </li>
        ))}
      </ul>
      {!done && batches.length > 0 && (
        <button type="button" disabled={loading} onClick={() => loadPage(cursor)}>
          {loading ? "Loading…" : "Load more"}
        </button>
      )}
      {loading && batches.length === 0 && <p className="muted">Loading…</p>}
    </div>
  );
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
