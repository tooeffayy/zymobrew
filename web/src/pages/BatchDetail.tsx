import { FormEvent, useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import {
  ApiError,
  Batch,
  BatchEvent,
  BatchEventPage,
  EventKind,
  Reminder,
  api,
} from "../api";

// Order matters: the kinds that advance reminder anchors come first
// because they're the actions a brewer does most often.
const EVENT_KINDS: EventKind[] = [
  "pitch", "rack", "bottle",
  "nutrient_addition", "degas", "addition",
  "stabilize", "backsweeten", "photo", "note",
];

// Reminder buckets: only "scheduled", "fired", and "snoozed" are
// active — completed/dismissed/cancelled drop out of the foreground
// list. Server returns all of them; UI filters here.
const ACTIVE_STATUSES = new Set<Reminder["status"]>(["scheduled", "fired", "snoozed"]);

export function BatchDetail() {
  const { id = "" } = useParams<{ id: string }>();
  const navigate = useNavigate();

  const [batch, setBatch] = useState<Batch | null>(null);
  const [reminders, setReminders] = useState<Reminder[]>([]);
  const [events, setEvents] = useState<BatchEvent[]>([]);
  const [loadError, setLoadError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);

  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<null | "delete">(null);
  const [showDone, setShowDone] = useState(false);

  const refetch = useCallback(async () => {
    try {
      const [b, rems, evs] = await Promise.all([
        api.get<Batch>(`/api/batches/${encodeURIComponent(id)}`),
        api.get<Reminder[]>(`/api/batches/${encodeURIComponent(id)}/reminders`),
        api.get<BatchEventPage>(`/api/batches/${encodeURIComponent(id)}/events`),
      ]);
      setBatch(b);
      setReminders(rems);
      setEvents(evs.events);
      setLoadError(null);
    } catch (e: unknown) {
      if (e instanceof ApiError) {
        setLoadError({ status: e.status, message: e.message });
      } else {
        setLoadError({ status: 0, message: "failed to load batch" });
      }
    }
  }, [id]);

  useEffect(() => {
    setLoading(true);
    refetch().finally(() => setLoading(false));
  }, [refetch]);

  if (loading) {
    return <div className="page"><p className="muted">Loading…</p></div>;
  }

  if (loadError || !batch) {
    return (
      <div className="page">
        <p className="back-link"><Link to="/batches">← Back to batches</Link></p>
        <h1>{loadError?.status === 404 ? "Batch not found" : "Couldn't load batch"}</h1>
        {loadError && loadError.status !== 404 && <p className="error">{loadError.message}</p>}
        <p className="muted">
          {loadError?.status === 404
            ? "It may have been deleted, or it isn't yours."
            : "Try again in a moment."}
        </p>
      </div>
    );
  }

  const onDelete = async () => {
    if (!window.confirm(`Delete "${batch.name}"? Readings, events, tasting notes, and reminders all go with it.`)) {
      return;
    }
    setActionError(null);
    setBusy("delete");
    try {
      await api.delete(`/api/batches/${encodeURIComponent(batch.id)}`);
      navigate("/batches", { replace: true });
    } catch (err) {
      setActionError(err instanceof ApiError ? err.message : "delete failed");
      setBusy(null);
    }
  };

  const active = reminders
    .filter((r) => ACTIVE_STATUSES.has(r.status))
    .sort((a, b) => +new Date(a.fire_at) - +new Date(b.fire_at));
  const done = reminders.filter((r) => r.status === "completed" || r.status === "dismissed");

  return (
    <div className="page">
      <p className="back-link"><Link to="/batches">← Back to batches</Link></p>

      <header className="recipe-header">
        <div className="recipe-header-meta">
          <span className={`pill pill-${batch.brew_type}`}>{batch.brew_type}</span>
          <span className={`stage stage-${batch.stage}`}>{batch.stage}</span>
          {batch.started_at && (
            <span className="muted">Started {fmtDate(batch.started_at)}</span>
          )}
          {batch.bottled_at && (
            <span className="muted">Bottled {fmtDate(batch.bottled_at)}</span>
          )}
          {batch.visibility !== "public" && (
            <span className="pill pill-visibility">{batch.visibility}</span>
          )}
        </div>
        <h1>{batch.name}</h1>
        {batch.notes && <p className="recipe-detail-desc">{batch.notes}</p>}
      </header>

      <div className="recipe-actions">
        <Link to={`/batches/${batch.id}/edit`} className="action-button">Edit</Link>
        <button
          type="button"
          className="action-button danger"
          onClick={onDelete}
          disabled={busy !== null}
        >
          {busy === "delete" ? "Deleting…" : "Delete"}
        </button>
      </div>

      {actionError && <p className="error">{actionError}</p>}

      <RemindersSection
        active={active}
        done={done}
        showDone={showDone}
        onToggleDone={() => setShowDone((v) => !v)}
        refetch={refetch}
        hasStartedAt={Boolean(batch.started_at)}
      />

      <EventsSection
        batchID={batch.id}
        events={events}
        refetch={refetch}
      />
    </div>
  );
}

// --- Reminders -----------------------------------------------------------

function RemindersSection({
  active, done, showDone, onToggleDone, refetch, hasStartedAt,
}: {
  active: Reminder[];
  done: Reminder[];
  showDone: boolean;
  onToggleDone: () => void;
  refetch: () => Promise<void>;
  hasStartedAt: boolean;
}) {
  return (
    <section className="recipe-section">
      <h2>Next steps</h2>
      {active.length === 0 ? (
        <p className="muted">
          {hasStartedAt
            ? "No active reminders. Log a pitch / rack / bottle event below to schedule the next set."
            : "No active reminders. Set a start time (or pin this batch to a recipe) to schedule reminder templates."}
        </p>
      ) : (
        <ul className="reminder-list">
          {active.map((r) => (
            <ReminderRow key={r.id} reminder={r} refetch={refetch} />
          ))}
        </ul>
      )}

      {done.length > 0 && (
        <div className="reminder-done-toggle">
          <button type="button" className="link-button" onClick={onToggleDone}>
            {showDone ? "Hide" : "Show"} {done.length} completed
          </button>
          {showDone && (
            <ul className="reminder-list reminder-list-done">
              {done.map((r) => (
                <li key={r.id} className="reminder-row reminder-done">
                  <div className="reminder-main">
                    <span className="reminder-title">{r.title}</span>
                    <span className="muted reminder-when">
                      {r.status === "completed" ? "Completed" : "Dismissed"}
                      {r.completed_at ? ` ${fmtRelative(r.completed_at)}` : ""}
                    </span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}

function ReminderRow({ reminder, refetch }: { reminder: Reminder; refetch: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const fire = new Date(reminder.fire_at);
  const overdue = fire.getTime() <= Date.now();

  // Reminders are mounted under the batch route:
  //   PATCH /api/batches/{batchId}/reminders/{reminderId}
  // server-side. Materialized reminders always carry a batch_id; the
  // empty-string fallback is a defensive guard for shapes we don't
  // expect to see here (ad-hoc user-only reminders aren't created
  // through this UI).
  const patchReminder = async (body: Record<string, unknown>) => {
    setBusy(true);
    setErr(null);
    try {
      await api.patch(
        `/api/batches/${encodeURIComponent(reminder.batch_id ?? "")}/reminders/${encodeURIComponent(reminder.id)}`,
        body,
      );
      await refetch();
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? e.message : "update failed");
      setBusy(false);
    }
  };

  const markDone = () => patchReminder({ status: "completed" });
  const dismiss  = () => patchReminder({ status: "dismissed" });
  const snoozeBy = (days: number) => {
    // If the reminder is already overdue, snooze relative to now —
    // otherwise "+1d" on a 2-week-old overdue reminder lands 13 days
    // in the past and the dispatcher fires it again immediately.
    const base = overdue ? new Date() : new Date(fire);
    base.setDate(base.getDate() + days);
    return patchReminder({ status: "snoozed", fire_at: base.toISOString() });
  };

  return (
    <li className={`reminder-row${overdue ? " reminder-overdue" : ""}`}>
      <div className="reminder-main">
        <span className="reminder-title">{reminder.title}</span>
        <span className="muted reminder-when">
          {overdue ? "Overdue " : ""}{fmtRelative(reminder.fire_at)}
        </span>
        {reminder.description && (
          <p className="reminder-desc">{reminder.description}</p>
        )}
        {err && <p className="error">{err}</p>}
      </div>
      <div className="reminder-actions">
        <button type="button" className="action-button" onClick={markDone} disabled={busy}>Done</button>
        <button type="button" className="action-button" onClick={() => snoozeBy(1)} disabled={busy} title="Snooze 1 day">+1d</button>
        <button type="button" className="action-button" onClick={() => snoozeBy(7)} disabled={busy} title="Snooze 1 week">+1w</button>
        <button type="button" className="action-button" onClick={dismiss} disabled={busy}>Dismiss</button>
      </div>
    </li>
  );
}

// --- Events --------------------------------------------------------------

function EventsSection({
  batchID, events, refetch,
}: {
  batchID: string;
  events: BatchEvent[];
  refetch: () => Promise<void>;
}) {
  const sorted = [...events].sort(
    (a, b) => +new Date(b.occurred_at) - +new Date(a.occurred_at),
  );
  return (
    <section className="recipe-section">
      <h2>Journal</h2>
      <LogEventForm batchID={batchID} onLogged={refetch} />
      {sorted.length === 0 ? (
        <p className="muted">No events yet — log pitch / rack / bottle as you go.</p>
      ) : (
        <ul className="event-list">
          {sorted.map((e) => (
            <li key={e.id} className="event-row">
              <div className="event-meta">
                <span className={`event-kind event-kind-${e.kind}`}>{e.kind.replace(/_/g, " ")}</span>
                <span className="muted event-when">{fmtRelative(e.occurred_at)}</span>
              </div>
              {e.title && <div className="event-title">{e.title}</div>}
              {e.description && <p className="event-desc">{e.description}</p>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function LogEventForm({
  batchID, onLogged,
}: {
  batchID: string;
  onLogged: () => Promise<void>;
}) {
  const [kind, setKind] = useState<EventKind>("note");
  const [occurredAt, setOccurredAt] = useState(toLocalInput(new Date()));
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const body: Record<string, unknown> = { kind };
      const iso = fromLocalInput(occurredAt);
      if (iso) body.occurred_at = iso;
      if (title.trim()) body.title = title.trim();
      if (description.trim()) body.description = description.trim();
      await api.post(`/api/batches/${encodeURIComponent(batchID)}/events`, body);
      setTitle("");
      setDescription("");
      // Reset occurred_at to "now" for the next log.
      setOccurredAt(toLocalInput(new Date()));
      await onLogged();
    } catch (e2: unknown) {
      setErr(e2 instanceof ApiError ? e2.message : "log failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="log-event-form">
      <div className="log-event-row">
        <select
          value={kind}
          onChange={(e) => setKind(e.target.value as EventKind)}
          aria-label="Event kind"
        >
          {EVENT_KINDS.map((k) => (
            <option key={k} value={k}>{k.replace(/_/g, " ")}</option>
          ))}
        </select>
        <input
          type="datetime-local"
          value={occurredAt}
          onChange={(e) => setOccurredAt(e.target.value)}
          aria-label="When"
        />
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Title (optional)"
          maxLength={200}
          aria-label="Title"
        />
        <button type="submit" disabled={busy}>
          {busy ? "Logging…" : "Log"}
        </button>
      </div>
      <textarea
        rows={2}
        value={description}
        onChange={(e) => setDescription(e.target.value)}
        placeholder="Description (optional)"
        maxLength={2048}
        aria-label="Description"
      />
      {err && <p className="error">{err}</p>}
    </form>
  );
}

// --- helpers -------------------------------------------------------------

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

// Relative time using Intl.RelativeTimeFormat — "in 3 days", "2 hours
// ago". Falls back to absolute date when the delta is over a year so
// "in 14 months" doesn't show up.
const RTF = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

function fmtRelative(iso: string, now: Date = new Date()): string {
  const target = new Date(iso);
  const deltaMs = target.getTime() - now.getTime();
  const abs = Math.abs(deltaMs);
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  const week = 7 * day;
  const year = 365 * day;

  if (abs < minute) return "just now";
  if (abs < hour)   return RTF.format(Math.round(deltaMs / minute), "minute");
  if (abs < day)    return RTF.format(Math.round(deltaMs / hour), "hour");
  if (abs < week)   return RTF.format(Math.round(deltaMs / day), "day");
  if (abs < year)   return RTF.format(Math.round(deltaMs / week), "week");
  return fmtDate(iso);
}

function toLocalInput(d: Date | string): string {
  const date = typeof d === "string" ? new Date(d) : d;
  if (Number.isNaN(date.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function fromLocalInput(local: string): string | null {
  if (!local) return null;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}
