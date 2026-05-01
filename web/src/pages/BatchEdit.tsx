import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { ApiError, Batch, api } from "../api";
import { BatchForm } from "../components/BatchForm";

// /batches/:id/edit — owner-only PATCH form. RequireAuth wraps the
// route; the server returns 404 for non-owner attempts so we just
// surface that as "not yours to edit".
export function BatchEdit() {
  const { id = "" } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [batch, setBatch] = useState<Batch | null>(null);
  const [error, setError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    setError(null);
    setBatch(null);
    api
      .get<Batch>(`/api/batches/${encodeURIComponent(id)}`)
      .then((b) => setBatch(b))
      .catch((e: unknown) => {
        if (e instanceof ApiError) {
          setError({ status: e.status, message: e.message });
        } else {
          setError({ status: 0, message: "failed to load batch" });
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

  if (error || !batch) {
    return (
      <div className="page">
        <p className="back-link"><Link to="/batches">← Back to batches</Link></p>
        <h1>Batch not found</h1>
        <p className="muted">It may have been deleted, or it's not yours to edit.</p>
      </div>
    );
  }

  return (
    <div className="page">
      <p className="back-link">
        <Link to={`/batches/${batch.id}`}>← Back to batch</Link>
      </p>
      <h1>Edit batch</h1>

      <BatchForm
        mode="edit"
        initial={batch}
        submitLabel="Save changes"
        submittingLabel="Saving…"
        cancelTo={`/batches/${batch.id}`}
        onSubmit={async (payload) => {
          const res = await api.patch<Batch>(`/api/batches/${encodeURIComponent(batch.id)}`, payload);
          navigate(`/batches/${res.id}`, { replace: true });
        }}
      />
    </div>
  );
}
