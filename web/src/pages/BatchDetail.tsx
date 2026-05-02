import { FormEvent, useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import {
  ApiError,
  Batch,
  BatchEvent,
  BatchEventPage,
  EventKind,
  Reading,
  ReadingPage,
  Reminder,
  TastingNote,
  TastingNotePage,
  api,
} from "../api";
import { Modal } from "../components/Modal";
import { ReadingsChart } from "../components/ReadingsChart";
import { TimeInput } from "../components/TimeInput";
import { fromCelsius, tempLabel, toCelsius, useTemperatureUnit } from "../units";

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
  const [readings, setReadings] = useState<Reading[]>([]);
  const [tastingNotes, setTastingNotes] = useState<TastingNote[]>([]);
  const [loadError, setLoadError] = useState<{ status: number; message: string } | null>(null);
  const [loading, setLoading] = useState(true);

  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<null | "delete">(null);
  const [showDone, setShowDone] = useState(false);

  const refetch = useCallback(async () => {
    try {
      const [b, rems, evs, rds, tns] = await Promise.all([
        api.get<Batch>(`/api/batches/${encodeURIComponent(id)}`),
        api.get<Reminder[]>(`/api/batches/${encodeURIComponent(id)}/reminders`),
        api.get<BatchEventPage>(`/api/batches/${encodeURIComponent(id)}/events`),
        api.get<ReadingPage>(`/api/batches/${encodeURIComponent(id)}/readings`),
        api.get<TastingNotePage>(`/api/batches/${encodeURIComponent(id)}/tasting-notes`),
      ]);
      setBatch(b);
      setReminders(rems);
      setEvents(evs.events);
      setReadings(rds.readings);
      setTastingNotes(tns.tasting_notes);
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

      <ReadingsSection
        batchID={batch.id}
        readings={readings}
        events={events}
        refetch={refetch}
      />

      <TastingNotesSection
        batchID={batch.id}
        notes={tastingNotes}
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
  // Single id at a time — opening a different row's edit closes the
  // previous one. Avoids two open editors fighting over the same data.
  const [editingID, setEditingID] = useState<string | null>(null);

  return (
    <section className="recipe-section">
      <h2>Journal</h2>
      <LogEventForm batchID={batchID} onLogged={refetch} />
      {sorted.length === 0 ? (
        <p className="muted">No events yet — log pitch / rack / bottle as you go.</p>
      ) : (
        <ul className="event-list">
          {sorted.map((e) => (
            <EventRow
              key={e.id}
              batchID={batchID}
              event={e}
              isEditing={editingID === e.id}
              onEdit={() => setEditingID(e.id)}
              onCancel={() => setEditingID(null)}
              onSaved={async () => {
                setEditingID(null);
                await refetch();
              }}
              onDeleted={refetch}
            />
          ))}
        </ul>
      )}
    </section>
  );
}

function EventRow({
  batchID, event, isEditing, onEdit, onCancel, onSaved, onDeleted,
}: {
  batchID: string;
  event: BatchEvent;
  isEditing: boolean;
  onEdit: () => void;
  onCancel: () => void;
  onSaved: () => Promise<void>;
  onDeleted: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onDelete = async () => {
    if (!window.confirm("Delete this entry?")) return;
    setBusy(true);
    setErr(null);
    try {
      await api.delete(
        `/api/batches/${encodeURIComponent(batchID)}/events/${encodeURIComponent(event.id)}`,
      );
      await onDeleted();
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? e.message : "delete failed");
      setBusy(false);
    }
  };

  if (isEditing) {
    return (
      <li className="event-row event-row-editing">
        <EditEventForm
          batchID={batchID}
          event={event}
          onCancel={onCancel}
          onSaved={onSaved}
        />
      </li>
    );
  }

  return (
    <li className="event-row">
      <div className="event-row-head">
        <div className="event-meta">
          <span className={`event-kind event-kind-${event.kind}`}>{event.kind.replace(/_/g, " ")}</span>
          <span className="muted event-when" title={fmtDateTime(event.occurred_at)}>
            {fmtRelative(event.occurred_at)}
          </span>
        </div>
        <div className="event-row-actions">
          <button type="button" className="link-button" onClick={onEdit} disabled={busy}>
            Edit
          </button>
          <button type="button" className="link-button event-delete" onClick={onDelete} disabled={busy}>
            {busy ? "Deleting…" : "Delete"}
          </button>
        </div>
      </div>
      {event.title && <div className="event-title">{event.title}</div>}
      {event.description && <p className="event-desc">{event.description}</p>}
      {err && <p className="error">{err}</p>}
    </li>
  );
}

function EditEventForm({
  batchID, event, onCancel, onSaved,
}: {
  batchID: string;
  event: BatchEvent;
  onCancel: () => void;
  onSaved: () => Promise<void>;
}) {
  const initial = new Date(event.occurred_at);
  const [kind, setKind] = useState<EventKind>(event.kind);
  const [occurredDate, setOccurredDate] = useState(toDateInput(initial));
  const [occurredTime, setOccurredTime] = useState(toTimeInput(initial));
  const [description, setDescription] = useState(event.description ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      // Build a partial PATCH — only send fields that actually changed.
      // Server uses COALESCE so omitted fields are left alone; keeps
      // network payload small and avoids re-running validation on
      // unchanged columns.
      const body: Record<string, unknown> = {};
      if (kind !== event.kind) body.kind = kind;
      const iso = combineDateTime(occurredDate, occurredTime);
      if (iso && iso !== new Date(event.occurred_at).toISOString()) {
        body.occurred_at = iso;
      }
      const trimmed = description.trim();
      if (trimmed !== (event.description ?? "")) {
        body.description = trimmed;
      }
      await api.patch(
        `/api/batches/${encodeURIComponent(batchID)}/events/${encodeURIComponent(event.id)}`,
        body,
      );
      await onSaved();
    } catch (e2: unknown) {
      setErr(e2 instanceof ApiError ? e2.message : "save failed");
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="log-event-form edit-event-form">
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
        <div className="datetime-pair">
          <input
            type="date"
            value={occurredDate}
            onChange={(e) => setOccurredDate(e.target.value)}
            aria-label="Date"
          />
          <TimeInput value={occurredTime} onChange={setOccurredTime} ariaLabel="Time" />
        </div>
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
      <div className="log-event-actions">
        <button type="button" className="cancel-link" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="submit" disabled={busy}>
          {busy ? "Saving…" : "Save"}
        </button>
      </div>
    </form>
  );
}

function LogEventForm({
  batchID, onLogged,
}: {
  batchID: string;
  onLogged: () => Promise<void>;
}) {
  const [kind, setKind] = useState<EventKind>("note");
  const [occurredDate, setOccurredDate] = useState(toDateInput(new Date()));
  const [occurredTime, setOccurredTime] = useState(toTimeInput(new Date()));
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const body: Record<string, unknown> = { kind };
      const iso = combineDateTime(occurredDate, occurredTime);
      if (iso) body.occurred_at = iso;
      if (description.trim()) body.description = description.trim();
      await api.post(`/api/batches/${encodeURIComponent(batchID)}/events`, body);
      setDescription("");
      // Reset occurred_at to "now" for the next log.
      const now = new Date();
      setOccurredDate(toDateInput(now));
      setOccurredTime(toTimeInput(now));
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
        <div className="datetime-pair">
          <input
            type="date"
            value={occurredDate}
            onChange={(e) => setOccurredDate(e.target.value)}
            aria-label="Date"
          />
          <TimeInput value={occurredTime} onChange={setOccurredTime} ariaLabel="Time" />
        </div>
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
      <div className="log-event-actions">
        <button type="submit" disabled={busy}>
          {busy ? "Logging…" : "Log"}
        </button>
      </div>
    </form>
  );
}

// --- Readings ------------------------------------------------------------

// Readings are a secondary surface — collapsed by default. The "what
// should I do next" timeline (reminders + events) leads the page; this
// is for brewers who want to log gravity/temp/pH alongside.
function ReadingsSection({
  batchID, readings, events, refetch,
}: {
  batchID: string;
  readings: Reading[];
  events: BatchEvent[];
  refetch: () => Promise<void>;
}) {
  const [open, setOpen] = useState(readings.length > 0);
  const [tempUnit] = useTemperatureUnit();
  const [editingId, setEditingId] = useState<string | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkErr, setBulkErr] = useState<string | null>(null);
  const [logOpen, setLogOpen] = useState(false);

  // Server returns ascending (chronological); the table renders newest
  // first so a freshly logged reading appears at the top.
  const sorted = [...readings].sort(
    (a, b) => +new Date(b.taken_at) - +new Date(a.taken_at),
  );

  // Drop selected ids that no longer exist (post-delete or post-refetch).
  // Cheap to recompute every render — readings is bounded.
  const validSelected = new Set(
    [...selected].filter((id) => readings.some((r) => r.id === id)),
  );

  const toggle = (id: string, checked: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id); else next.delete(id);
      return next;
    });
  };
  const selectAll = (checked: boolean) => {
    setSelected(checked ? new Set(readings.map((r) => r.id)) : new Set());
  };
  const onBulkDelete = async () => {
    const ids = [...validSelected];
    if (ids.length === 0) return;
    if (!window.confirm(`Delete ${ids.length} reading${ids.length === 1 ? "" : "s"}?`)) return;
    setBulkBusy(true);
    setBulkErr(null);
    try {
      await api.delete(`/api/batches/${encodeURIComponent(batchID)}/readings`, { ids });
      setSelected(new Set());
      setEditingId(null);
      await refetch();
    } catch (e) {
      setBulkErr(e instanceof ApiError ? e.message : "delete failed");
    } finally {
      setBulkBusy(false);
    }
  };

  const allChecked = readings.length > 0 && validSelected.size === readings.length;
  const someChecked = validSelected.size > 0 && !allChecked;

  return (
    <section className="recipe-section readings-section">
      <button
        type="button"
        className="link-button section-toggle"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <h2>Readings <span className="muted">({readings.length})</span></h2>
        <span className="section-toggle-chevron">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <>
          <ReadingsChart readings={readings} events={events} />
          {/* Toolbar is always present — left side hosts "+ Add reading"
              (the entry point for new readings, replacing the inline
              form), right side hosts bulk actions when something is
              selected. Single anchor row above the table keeps the
              table position stable across all states. */}
          <div className="bulk-toolbar bulk-toolbar-resting">
            <button
              type="button"
              className="link-button bulk-toolbar-add"
              onClick={() => setLogOpen(true)}
            >
              + Add reading
            </button>
            {validSelected.size > 0 && (
              <>
                <span className="bulk-toolbar-divider" aria-hidden="true" />
                <span className="bulk-toolbar-label">
                  {validSelected.size} selected
                </span>
                <button
                  type="button"
                  className="link-button event-delete"
                  onClick={onBulkDelete}
                  disabled={bulkBusy}
                >
                  {bulkBusy ? "Deleting…" : "Delete selected"}
                </button>
                <button
                  type="button"
                  className="link-button"
                  onClick={() => setSelected(new Set())}
                  disabled={bulkBusy}
                >
                  Clear
                </button>
                {bulkErr && <span className="error bulk-toolbar-error">{bulkErr}</span>}
              </>
            )}
          </div>
          {sorted.length === 0 ? (
            <p className="muted">No readings yet — click <em>+ Add reading</em> above to log gravity, temperature, or pH.</p>
          ) : (
            <>
              <table className="readings-table">
                <colgroup>
                  <col className="col-check" />
                  <col className="col-when" />
                  <col className="col-num" />
                  <col className="col-num" />
                  <col className="col-num" />
                  <col />
                  <col className="col-actions" />
                </colgroup>
                <thead>
                  <tr>
                    <th className="col-check-th">
                      <input
                        type="checkbox"
                        aria-label="Select all readings"
                        checked={allChecked}
                        ref={(el) => { if (el) el.indeterminate = someChecked; }}
                        onChange={(e) => selectAll(e.target.checked)}
                      />
                    </th>
                    <th>When</th>
                    <th>Gravity</th>
                    <th>Temp {tempLabel(tempUnit)}</th>
                    <th>pH</th>
                    <th>Notes</th>
                    <th aria-label="Actions" />
                  </tr>
                </thead>
                <tbody>
                  {sorted.map((r) => (
                    <ReadingRow
                      key={r.id}
                      batchID={batchID}
                      reading={r}
                      isEditing={editingId === r.id}
                      isSelected={validSelected.has(r.id)}
                      onSelectChange={(checked) => toggle(r.id, checked)}
                      onEdit={() => setEditingId(r.id)}
                      onCancel={() => setEditingId(null)}
                      onSaved={async () => { setEditingId(null); await refetch(); }}
                      onDeleted={async () => { setEditingId(null); await refetch(); }}
                    />
                  ))}
                </tbody>
              </table>
            </>
          )}
          <Modal
            isOpen={logOpen}
            onOpenChange={setLogOpen}
            title="Log a reading"
          >
            {(close) => (
              <LogReadingForm
                batchID={batchID}
                onLogged={async () => {
                  await refetch();
                  close();
                }}
              />
            )}
          </Modal>
        </>
      )}
    </section>
  );
}

function ReadingRow({
  batchID, reading, isEditing, isSelected, onSelectChange, onEdit, onCancel, onSaved, onDeleted,
}: {
  batchID: string;
  reading: Reading;
  isEditing: boolean;
  isSelected: boolean;
  onSelectChange: (checked: boolean) => void;
  onEdit: () => void;
  onCancel: () => void;
  onSaved: () => Promise<void>;
  onDeleted: () => Promise<void>;
}) {
  const [tempUnit] = useTemperatureUnit();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  if (isEditing) {
    // The editing row owns the row layout (it has its own colspan-aware
    // markup), so the bulk-select checkbox cell is rendered empty.
    return (
      <EditReadingRow
        batchID={batchID}
        reading={reading}
        onCancel={onCancel}
        onSaved={onSaved}
      />
    );
  }

  const onDelete = async () => {
    if (!window.confirm("Delete this reading?")) return;
    setBusy(true);
    setErr(null);
    try {
      await api.delete(
        `/api/batches/${encodeURIComponent(batchID)}/readings/${encodeURIComponent(reading.id)}`,
      );
      await onDeleted();
    } catch (e: unknown) {
      setErr(e instanceof ApiError ? e.message : "delete failed");
      setBusy(false);
    }
  };

  return (
    <tr className={isSelected ? "reading-row-selected" : undefined}>
      <td className="col-check-td">
        <input
          type="checkbox"
          aria-label={`Select reading from ${fmtDateTime(reading.taken_at)}`}
          checked={isSelected}
          onChange={(e) => onSelectChange(e.target.checked)}
        />
      </td>
      <td className="reading-when">{fmtDateTime(reading.taken_at)}</td>
      <td className="reading-num">{fmtGravity(reading.gravity)}</td>
      <td className="reading-num">
        {fmtNum(
          typeof reading.temperature_c === "number"
            ? fromCelsius(reading.temperature_c, tempUnit)
            : undefined,
          1,
        )}
      </td>
      <td className="reading-num">{fmtNum(reading.ph, 2)}</td>
      <td>
        {reading.notes ?? ""}
        {err && <span className="error reading-row-error"> {err}</span>}
      </td>
      <td className="reading-actions">
        <button type="button" className="link-button" onClick={onEdit} disabled={busy}>
          Edit
        </button>
        <button type="button" className="link-button event-delete" onClick={onDelete} disabled={busy}>
          {busy ? "…" : "Delete"}
        </button>
      </td>
    </tr>
  );
}

function EditReadingRow({
  batchID, reading, onCancel, onSaved,
}: {
  batchID: string;
  reading: Reading;
  onCancel: () => void;
  onSaved: () => Promise<void>;
}) {
  const [tempUnit] = useTemperatureUnit();
  const initial = new Date(reading.taken_at);
  const [takenDate, setTakenDate] = useState(toDateInput(initial));
  const [takenTime, setTakenTime] = useState(toTimeInput(initial));
  const [gravity, setGravity] = useState(
    typeof reading.gravity === "number" ? String(reading.gravity) : "",
  );
  const [temp, setTemp] = useState(
    typeof reading.temperature_c === "number"
      ? fromCelsius(reading.temperature_c, tempUnit).toFixed(1)
      : "",
  );
  const [ph, setPh] = useState(
    typeof reading.ph === "number" ? String(reading.ph) : "",
  );
  const [notes, setNotes] = useState(reading.notes ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      // COALESCE PATCH: only send fields the user actually changed. Empty
      // strings are treated as "no change" for numerics — fields can't be
      // cleared to NULL via PATCH (per API invariants in CLAUDE.md), and
      // an empty input expresses intent to leave alone, not clear.
      const body: Record<string, unknown> = {};
      const iso = combineDateTime(takenDate, takenTime);
      if (iso && iso !== initial.toISOString()) body.taken_at = iso;

      const g = parseNum(gravity);
      if (g !== null && g !== reading.gravity) body.gravity = g;

      const t = parseNum(temp);
      if (t !== null) {
        const asC = toCelsius(t, tempUnit);
        if (asC !== reading.temperature_c) body.temperature_c = asC;
      }

      const p = parseNum(ph);
      if (p !== null && p !== reading.ph) body.ph = p;

      if (notes !== (reading.notes ?? "")) body.notes = notes;

      if (Object.keys(body).length > 0) {
        await api.patch(
          `/api/batches/${encodeURIComponent(batchID)}/readings/${encodeURIComponent(reading.id)}`,
          body,
        );
      }
      await onSaved();
    } catch (e2: unknown) {
      setErr(e2 instanceof ApiError ? e2.message : "save failed");
      setBusy(false);
    }
  };

  return (
    <tr className="reading-row-editing">
      <td className="col-check-td" aria-hidden="true" />
      <td className="reading-when">
        <div className="datetime-pair reading-edit-when">
          <input
            type="date"
            value={takenDate}
            onChange={(e) => setTakenDate(e.target.value)}
            aria-label="Date"
          />
          <TimeInput value={takenTime} onChange={setTakenTime} ariaLabel="Time" />
        </div>
      </td>
      <td className="reading-num">
        <input
          type="number"
          step="0.001"
          min="0.99"
          max="1.2"
          inputMode="decimal"
          value={gravity}
          onChange={(e) => setGravity(e.target.value)}
          aria-label="Gravity"
        />
      </td>
      <td className="reading-num">
        <input
          type="number"
          step="0.1"
          inputMode="decimal"
          value={temp}
          onChange={(e) => setTemp(e.target.value)}
          aria-label={tempUnit === "F" ? "Temperature in Fahrenheit" : "Temperature in Celsius"}
        />
      </td>
      <td className="reading-num">
        <input
          type="number"
          step="0.01"
          min="0"
          max="14"
          inputMode="decimal"
          value={ph}
          onChange={(e) => setPh(e.target.value)}
          aria-label="pH"
        />
      </td>
      <td>
        <input
          type="text"
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          maxLength={1024}
          aria-label="Notes"
        />
        {err && <p className="error reading-row-error">{err}</p>}
      </td>
      <td className="reading-actions">
        <form onSubmit={onSubmit} className="reading-edit-actions">
          <button type="submit" className="link-button" disabled={busy}>
            {busy ? "…" : "Save"}
          </button>
          <button type="button" className="link-button cancel-link" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        </form>
      </td>
    </tr>
  );
}

function LogReadingForm({
  batchID, onLogged,
}: {
  batchID: string;
  onLogged: () => Promise<void>;
}) {
  const [takenDate, setTakenDate] = useState(toDateInput(new Date()));
  const [takenTime, setTakenTime] = useState(toTimeInput(new Date()));
  const [gravity, setGravity] = useState("");
  const [temp, setTemp] = useState("");
  const [ph, setPh] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [tempUnit] = useTemperatureUnit();

  // Mirrors the server-side guard (handleCreateReading) — at least one
  // measurement is required. Cheap to enforce here so the submit button
  // can stay disabled until something's filled in.
  const hasAny =
    gravity.trim() !== "" || temp.trim() !== "" || ph.trim() !== "";

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const body: Record<string, unknown> = {};
      const iso = combineDateTime(takenDate, takenTime);
      if (iso) body.taken_at = iso;
      const g = parseNum(gravity);
      const t = parseNum(temp);
      const p = parseNum(ph);
      if (g !== null) body.gravity = g;
      // Always send Celsius — that's the canonical storage. F input is
      // converted unrounded so a "70°F" entry round-trips to "70.0°F"
      // on display rather than drifting via intermediate truncation.
      if (t !== null) body.temperature_c = toCelsius(t, tempUnit);
      if (p !== null) body.ph = p;
      if (notes.trim()) body.notes = notes.trim();
      await api.post(`/api/batches/${encodeURIComponent(batchID)}/readings`, body);
      setGravity("");
      setTemp("");
      setPh("");
      setNotes("");
      const now = new Date();
      setTakenDate(toDateInput(now));
      setTakenTime(toTimeInput(now));
      await onLogged();
    } catch (e2: unknown) {
      setErr(e2 instanceof ApiError ? e2.message : "log failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="log-reading-form">
      <div className="log-reading-row">
        <div className="datetime-pair">
          <input
            type="date"
            value={takenDate}
            onChange={(e) => setTakenDate(e.target.value)}
            aria-label="Date"
          />
          <TimeInput value={takenTime} onChange={setTakenTime} ariaLabel="Time" />
        </div>
        <input
          type="number"
          step="0.001"
          min="0.99"
          max="1.2"
          inputMode="decimal"
          value={gravity}
          onChange={(e) => setGravity(e.target.value)}
          placeholder="Gravity"
          aria-label="Gravity"
        />
        <input
          type="number"
          step="0.1"
          inputMode="decimal"
          value={temp}
          onChange={(e) => setTemp(e.target.value)}
          placeholder={`Temp ${tempLabel(tempUnit)}`}
          aria-label={tempUnit === "F" ? "Temperature in Fahrenheit" : "Temperature in Celsius"}
        />
        <input
          type="number"
          step="0.01"
          min="0"
          max="14"
          inputMode="decimal"
          value={ph}
          onChange={(e) => setPh(e.target.value)}
          placeholder="pH"
          aria-label="pH"
        />
      </div>
      <input
        type="text"
        value={notes}
        onChange={(e) => setNotes(e.target.value)}
        placeholder="Notes (optional)"
        maxLength={1024}
        aria-label="Notes"
      />
      {err && <p className="error">{err}</p>}
      <div className="log-reading-actions">
        <button type="submit" disabled={busy || !hasAny}>
          {busy ? "Logging…" : "Log reading"}
        </button>
      </div>
    </form>
  );
}

// --- Tasting notes -------------------------------------------------------

function TastingNotesSection({
  batchID, notes, refetch,
}: {
  batchID: string;
  notes: TastingNote[];
  refetch: () => Promise<void>;
}) {
  const [open, setOpen] = useState(notes.length > 0);

  // Server returns newest first. Keep that order — most-recent tastings
  // are what brewers reach for as the brew matures.
  return (
    <section className="recipe-section tasting-section">
      <button
        type="button"
        className="link-button section-toggle"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        <h2>Tasting notes <span className="muted">({notes.length})</span></h2>
        <span className="section-toggle-chevron">{open ? "▾" : "▸"}</span>
      </button>
      {open && (
        <>
          <LogTastingNoteForm batchID={batchID} onLogged={refetch} />
          {notes.length === 0 ? (
            <p className="muted">No tastings yet — add one as the batch matures.</p>
          ) : (
            <ul className="tasting-list">
              {notes.map((n) => (
                <li key={n.id} className="tasting-row">
                  <div className="tasting-header">
                    <span className="muted tasting-when">{fmtDateTime(n.tasted_at)}</span>
                    {typeof n.rating === "number" && (
                      <span className="tasting-rating" aria-label={`Rating ${n.rating} of 5`}>
                        {"★".repeat(n.rating)}
                        <span className="tasting-rating-empty">{"★".repeat(5 - n.rating)}</span>
                      </span>
                    )}
                  </div>
                  <dl className="tasting-fields">
                    {n.aroma     && (<><dt>Aroma</dt><dd>{n.aroma}</dd></>)}
                    {n.flavor    && (<><dt>Flavor</dt><dd>{n.flavor}</dd></>)}
                    {n.mouthfeel && (<><dt>Mouthfeel</dt><dd>{n.mouthfeel}</dd></>)}
                    {n.finish    && (<><dt>Finish</dt><dd>{n.finish}</dd></>)}
                    {n.notes     && (<><dt>Notes</dt><dd>{n.notes}</dd></>)}
                  </dl>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function LogTastingNoteForm({
  batchID, onLogged,
}: {
  batchID: string;
  onLogged: () => Promise<void>;
}) {
  const [tastedDate, setTastedDate] = useState(toDateInput(new Date()));
  const [tastedTime, setTastedTime] = useState(toTimeInput(new Date()));
  const [rating, setRating] = useState<number | null>(null);
  const [aroma, setAroma] = useState("");
  const [flavor, setFlavor] = useState("");
  const [mouthfeel, setMouthfeel] = useState("");
  const [finish, setFinish] = useState("");
  const [notes, setNotes] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Server requires at least one field; mirror the guard so the button
  // stays disabled until there's something to submit.
  const hasAny =
    rating !== null ||
    aroma.trim() !== "" || flavor.trim() !== "" ||
    mouthfeel.trim() !== "" || finish.trim() !== "" ||
    notes.trim() !== "";

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const body: Record<string, unknown> = {};
      const iso = combineDateTime(tastedDate, tastedTime);
      if (iso) body.tasted_at = iso;
      if (rating !== null) body.rating = rating;
      if (aroma.trim())     body.aroma = aroma.trim();
      if (flavor.trim())    body.flavor = flavor.trim();
      if (mouthfeel.trim()) body.mouthfeel = mouthfeel.trim();
      if (finish.trim())    body.finish = finish.trim();
      if (notes.trim())     body.notes = notes.trim();
      await api.post(`/api/batches/${encodeURIComponent(batchID)}/tasting-notes`, body);
      setRating(null);
      setAroma("");
      setFlavor("");
      setMouthfeel("");
      setFinish("");
      setNotes("");
      const now = new Date();
      setTastedDate(toDateInput(now));
      setTastedTime(toTimeInput(now));
      await onLogged();
    } catch (e2: unknown) {
      setErr(e2 instanceof ApiError ? e2.message : "save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="log-tasting-form">
      <div className="log-tasting-head">
        <div className="datetime-pair">
          <input
            type="date"
            value={tastedDate}
            onChange={(e) => setTastedDate(e.target.value)}
            aria-label="Date tasted"
          />
          <TimeInput value={tastedTime} onChange={setTastedTime} ariaLabel="Time tasted" />
        </div>
        <RatingInput value={rating} onChange={setRating} />
      </div>

      <div className="log-tasting-grid">
        <label className="field">
          <span>Aroma</span>
          <input type="text" value={aroma} onChange={(e) => setAroma(e.target.value)} maxLength={1024} />
        </label>
        <label className="field">
          <span>Flavor</span>
          <input type="text" value={flavor} onChange={(e) => setFlavor(e.target.value)} maxLength={1024} />
        </label>
        <label className="field">
          <span>Mouthfeel</span>
          <input type="text" value={mouthfeel} onChange={(e) => setMouthfeel(e.target.value)} maxLength={1024} />
        </label>
        <label className="field">
          <span>Finish</span>
          <input type="text" value={finish} onChange={(e) => setFinish(e.target.value)} maxLength={1024} />
        </label>
      </div>

      <label className="field">
        <span>Notes</span>
        <textarea
          rows={2}
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          maxLength={4096}
          placeholder="Anything else worth remembering."
        />
      </label>

      {err && <p className="error">{err}</p>}

      <div className="form-actions">
        <button type="submit" disabled={busy || !hasAny}>
          {busy ? "Saving…" : "Save tasting note"}
        </button>
      </div>
    </form>
  );
}

function RatingInput({
  value, onChange,
}: {
  value: number | null;
  onChange: (v: number | null) => void;
}) {
  return (
    <div className="rating-input" role="radiogroup" aria-label="Rating">
      {[1, 2, 3, 4, 5].map((n) => {
        const active = value !== null && n <= value;
        return (
          <button
            key={n}
            type="button"
            role="radio"
            aria-checked={value === n}
            aria-label={`${n} star${n === 1 ? "" : "s"}`}
            // Click-on-active toggles off so a rating can be cleared
            // without an explicit "no rating" button.
            onClick={() => onChange(value === n ? null : n)}
            className={`rating-star${active ? " rating-star-active" : ""}`}
          >
            ★
          </button>
        );
      })}
      {value !== null && (
        <button type="button" className="link-button rating-clear" onClick={() => onChange(null)}>
          clear
        </button>
      )}
    </div>
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

// `<input type="date">` speaks "YYYY-MM-DD", `<input type="time">`
// speaks "HH:mm" — both in the local tz. We split here (not
// datetime-local) because the native datetime-local widget renders
// chunkier than two side-by-side fields.
function toDateInput(d: Date): string {
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function toTimeInput(d: Date): string {
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// Combine the two field values back to RFC3339 (with the offset baked
// in via toISOString). If date is missing we send nothing — the server
// defaults occurred_at/tasted_at/taken_at to now. If time is missing
// but date is set, we treat it as midnight local — same convention
// as the native datetime-local widget when only the date segment is
// filled.
function combineDateTime(date: string, time: string): string | null {
  if (!date) return null;
  const t = time || "00:00";
  const d = new Date(`${date}T${t}`);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}

function fmtDateTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
      hour: "2-digit",
      minute: "2-digit",
    });
  } catch {
    return iso;
  }
}

// Gravity is conventionally written to 3 decimals (1.085, 0.998).
function fmtGravity(n: number | undefined): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "";
  return n.toFixed(3);
}

function fmtNum(n: number | undefined, places: number): string {
  if (n === undefined || n === null || Number.isNaN(n)) return "";
  return n.toFixed(places);
}

function parseNum(s: string): number | null {
  const t = s.trim();
  if (t === "") return null;
  const n = Number(t);
  return Number.isFinite(n) ? n : null;
}
