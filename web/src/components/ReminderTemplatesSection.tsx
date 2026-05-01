import { FormEvent, useCallback, useEffect, useState } from "react";

import {
  ApiError,
  EventKind,
  ReminderAnchor,
  ReminderTemplate,
  api,
} from "../api";

// Recipe reminder templates surface. Public to anyone who can see the
// recipe (server enforces 404 on private-not-yours); editable only by
// the owner. The flag is an explicit prop rather than a re-derive from
// useAuth so this component is reusable wherever ownership has already
// been resolved.

// Anchors selectable on a template. `absolute` is omitted because the
// server rejects it for templates — that's a runtime constraint, not a
// type-level one, so the option is dropped here rather than on the
// type. `custom_event` is included since the API accepts it, but the
// CLAUDE.md "Known deferred gaps" note flags that materialization
// isn't wired yet — surfaced via helper text on the form.
const TEMPLATE_ANCHORS: ReminderAnchor[] = [
  "batch_start", "pitch", "rack", "bottle", "custom_event",
];

const ANCHOR_LABEL: Record<ReminderAnchor, string> = {
  absolute:     "absolute time",
  batch_start:  "batch start",
  pitch:        "pitch",
  rack:         "rack",
  bottle:       "bottle",
  custom_event: "custom event",
};

// Suggested event kind = the action the user is most likely to log when
// they complete the reminder. Same list as the batch-detail event
// selector, in the same priority order.
const EVENT_KINDS: EventKind[] = [
  "rack", "bottle", "pitch",
  "nutrient_addition", "degas", "addition",
  "stabilize", "backsweeten", "photo", "note",
];

type OffsetUnit = "minutes" | "hours" | "days" | "weeks";

const UNIT_TO_MIN: Record<OffsetUnit, number> = {
  minutes: 1,
  hours:   60,
  days:    60 * 24,
  weeks:   60 * 24 * 7,
};

// Pick the largest unit that divides the offset cleanly so a "14 days"
// edit doesn't come back as "20160 minutes". Falls back to minutes.
function decomposeOffset(min: number): { magnitude: number; unit: OffsetUnit; sign: 1 | -1 } {
  const sign = min < 0 ? -1 : 1;
  const abs = Math.abs(min);
  if (abs === 0) return { magnitude: 0, unit: "days", sign: 1 };
  const tries: OffsetUnit[] = ["weeks", "days", "hours", "minutes"];
  for (const u of tries) {
    if (abs % UNIT_TO_MIN[u] === 0) {
      return { magnitude: abs / UNIT_TO_MIN[u], unit: u, sign };
    }
  }
  return { magnitude: abs, unit: "minutes", sign };
}

function composeOffset(magnitude: number, unit: OffsetUnit, sign: 1 | -1): number {
  return sign * magnitude * UNIT_TO_MIN[unit];
}

// Render an offset as a human phrase: "1 day after pitch", "2 hours before rack".
// Zero offsets render as "at <anchor>".
function formatOffset(min: number, anchor: ReminderAnchor): string {
  if (min === 0) return `at ${ANCHOR_LABEL[anchor]}`;
  const { magnitude, unit, sign } = decomposeOffset(min);
  const unitLabel = magnitude === 1 ? unit.replace(/s$/, "") : unit;
  return `${magnitude} ${unitLabel} ${sign === 1 ? "after" : "before"} ${ANCHOR_LABEL[anchor]}`;
}

export function ReminderTemplatesSection({
  recipeID, isOwner,
}: {
  recipeID: string;
  isOwner: boolean;
}) {
  const [templates, setTemplates] = useState<ReminderTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [editingID, setEditingID] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const refetch = useCallback(async () => {
    setLoadError(null);
    try {
      const list = await api.get<ReminderTemplate[]>(
        `/api/recipes/${encodeURIComponent(recipeID)}/reminder-templates`,
      );
      setTemplates(list);
    } catch (e) {
      setLoadError(e instanceof ApiError ? e.message : "failed to load templates");
    }
  }, [recipeID]);

  useEffect(() => {
    setLoading(true);
    refetch().finally(() => setLoading(false));
  }, [refetch]);

  const sorted = [...templates].sort((a, b) =>
    a.sort_order - b.sort_order || a.title.localeCompare(b.title),
  );

  return (
    <section className="recipe-section template-section">
      <div className="template-section-head">
        <h2>Reminder schedule</h2>
        {isOwner && !adding && (
          <button
            type="button"
            className="action-button"
            onClick={() => { setAdding(true); setEditingID(null); }}
          >
            + Add reminder
          </button>
        )}
      </div>

      <p className="muted template-help">
        These reminders auto-materialize when a batch is created from this recipe — pinned to the batch's pitch / rack / bottle events as they happen.
      </p>

      {loading ? (
        <p className="muted">Loading…</p>
      ) : loadError ? (
        <p className="error">{loadError}</p>
      ) : (
        <>
          {adding && (
            <TemplateForm
              recipeID={recipeID}
              onCancel={() => setAdding(false)}
              onSaved={async () => { setAdding(false); await refetch(); }}
            />
          )}

          {sorted.length === 0 && !adding ? (
            <p className="muted template-empty">
              {isOwner
                ? "No reminders set up yet. Add one and any future batch from this recipe will pick it up automatically."
                : "No reminders on this recipe."}
            </p>
          ) : (
            <ul className="template-list">
              {sorted.map((t) => (
                <TemplateRow
                  key={t.id}
                  recipeID={recipeID}
                  template={t}
                  isOwner={isOwner}
                  isEditing={editingID === t.id}
                  onEdit={() => { setEditingID(t.id); setAdding(false); }}
                  onCancel={() => setEditingID(null)}
                  onSaved={async () => { setEditingID(null); await refetch(); }}
                  onDeleted={refetch}
                />
              ))}
            </ul>
          )}
        </>
      )}
    </section>
  );
}

function TemplateRow({
  recipeID, template, isOwner, isEditing, onEdit, onCancel, onSaved, onDeleted,
}: {
  recipeID: string;
  template: ReminderTemplate;
  isOwner: boolean;
  isEditing: boolean;
  onEdit: () => void;
  onCancel: () => void;
  onSaved: () => Promise<void>;
  onDeleted: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  if (isEditing) {
    return (
      <li className="template-row template-row-editing">
        <TemplateForm
          recipeID={recipeID}
          template={template}
          onCancel={onCancel}
          onSaved={onSaved}
        />
      </li>
    );
  }

  const onDelete = async () => {
    if (!window.confirm(`Delete reminder "${template.title}"? Existing batches keep their already-materialized reminders.`)) {
      return;
    }
    setBusy(true);
    setErr(null);
    try {
      await api.delete(
        `/api/recipes/${encodeURIComponent(recipeID)}/reminder-templates/${encodeURIComponent(template.id)}`,
      );
      await onDeleted();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "delete failed");
      setBusy(false);
    }
  };

  return (
    <li className="template-row">
      <div className="template-main">
        <div className="template-title-row">
          <span className="template-title">{template.title}</span>
          <span className="muted template-when">{formatOffset(template.offset_minutes, template.anchor)}</span>
        </div>
        {template.description && <p className="template-desc">{template.description}</p>}
        {template.suggested_event_kind && (
          <p className="muted template-suggest">
            Suggests logging a <code>{template.suggested_event_kind.replace(/_/g, " ")}</code> event when done.
          </p>
        )}
        {err && <p className="error">{err}</p>}
      </div>
      {isOwner && (
        <div className="template-actions">
          <button type="button" className="link-button" onClick={onEdit} disabled={busy}>Edit</button>
          <button type="button" className="link-button template-delete" onClick={onDelete} disabled={busy}>
            {busy ? "Deleting…" : "Delete"}
          </button>
        </div>
      )}
    </li>
  );
}

function TemplateForm({
  recipeID, template, onCancel, onSaved,
}: {
  recipeID: string;
  template?: ReminderTemplate;
  onCancel: () => void;
  onSaved: () => Promise<void>;
}) {
  const isEdit = !!template;
  const initial = template
    ? decomposeOffset(template.offset_minutes)
    : { magnitude: 1, unit: "days" as OffsetUnit, sign: 1 as 1 | -1 };

  const [title, setTitle]               = useState(template?.title ?? "");
  const [description, setDescription]   = useState(template?.description ?? "");
  const [anchor, setAnchor]             = useState<ReminderAnchor>(template?.anchor ?? "pitch");
  const [magnitude, setMagnitude]       = useState(String(initial.magnitude));
  const [unit, setUnit]                 = useState<OffsetUnit>(initial.unit);
  const [sign, setSign]                 = useState<1 | -1>(initial.sign);
  const [suggestedKind, setSuggestedKind] = useState<EventKind | "">(template?.suggested_event_kind ?? "");
  const [sortOrder, setSortOrder]       = useState(String(template?.sort_order ?? 0));

  const [busy, setBusy] = useState(false);
  const [err, setErr]   = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);

    const trimmedTitle = title.trim();
    if (!trimmedTitle) {
      setErr("Title is required.");
      return;
    }
    const mag = Number(magnitude);
    if (!Number.isFinite(mag) || mag < 0) {
      setErr("Offset must be a non-negative number.");
      return;
    }
    const sortOrderNum = Number(sortOrder);
    if (!Number.isFinite(sortOrderNum)) {
      setErr("Sort order must be a number.");
      return;
    }
    const offsetMin = composeOffset(mag, unit, sign);

    setBusy(true);
    try {
      const trimmedDesc = description.trim();
      if (isEdit && template) {
        // PATCH only the changed fields. The server's COALESCE pattern
        // means omitted fields stay as-is — keeping the payload tight
        // also avoids re-validating values the user didn't touch.
        const body: Record<string, unknown> = {};
        if (trimmedTitle !== template.title) body.title = trimmedTitle;
        if (trimmedDesc !== (template.description ?? "")) body.description = trimmedDesc;
        if (anchor !== template.anchor) body.anchor = anchor;
        if (offsetMin !== template.offset_minutes) body.offset_minutes = offsetMin;
        // Only PATCH suggested_event_kind when *setting* it to a real
        // value. The server's PATCH currently can't null this column
        // (CLAUDE.md: "Fields cannot be cleared to NULL yet") — null
        // would be silently ignored and "" would 422. The UI dropdown
        // still shows "— none —" for clarity, but picking it on an
        // existing template is a no-op; users who really need to clear
        // delete + re-create.
        if (suggestedKind && suggestedKind !== template.suggested_event_kind) {
          body.suggested_event_kind = suggestedKind;
        }
        if (sortOrderNum !== template.sort_order) body.sort_order = sortOrderNum;
        await api.patch(
          `/api/recipes/${encodeURIComponent(recipeID)}/reminder-templates/${encodeURIComponent(template.id)}`,
          body,
        );
      } else {
        const body: Record<string, unknown> = {
          title: trimmedTitle,
          anchor,
          offset_minutes: offsetMin,
          sort_order: sortOrderNum,
        };
        if (trimmedDesc) body.description = trimmedDesc;
        if (suggestedKind) body.suggested_event_kind = suggestedKind;
        await api.post(
          `/api/recipes/${encodeURIComponent(recipeID)}/reminder-templates`,
          body,
        );
      }
      await onSaved();
    } catch (e2) {
      setErr(e2 instanceof ApiError ? e2.message : "save failed");
      setBusy(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="template-form">
      <label className="field">
        <span>Title</span>
        <input
          type="text"
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          maxLength={200}
          placeholder="Check gravity"
          autoFocus
          required
        />
      </label>

      <div className="template-when-row">
        <label className="field">
          <span>When</span>
          <div className="template-when-controls">
            <input
              type="number"
              min="0"
              step="1"
              inputMode="numeric"
              value={magnitude}
              onChange={(e) => setMagnitude(e.target.value)}
              className="template-magnitude"
              aria-label="Offset magnitude"
            />
            <select
              value={unit}
              onChange={(e) => setUnit(e.target.value as OffsetUnit)}
              aria-label="Offset unit"
            >
              <option value="minutes">minutes</option>
              <option value="hours">hours</option>
              <option value="days">days</option>
              <option value="weeks">weeks</option>
            </select>
            <select
              value={String(sign)}
              onChange={(e) => setSign(e.target.value === "-1" ? -1 : 1)}
              aria-label="Direction"
            >
              <option value="1">after</option>
              <option value="-1">before</option>
            </select>
            <select
              value={anchor}
              onChange={(e) => setAnchor(e.target.value as ReminderAnchor)}
              aria-label="Anchor event"
            >
              {TEMPLATE_ANCHORS.map((a) => (
                <option key={a} value={a}>{ANCHOR_LABEL[a]}</option>
              ))}
            </select>
          </div>
        </label>
      </div>

      {anchor === "custom_event" && (
        <p className="muted template-help">
          Custom-event anchors are accepted but won't materialize until the custom-event selector ships.
        </p>
      )}

      <label className="field">
        <span>Description</span>
        <textarea
          rows={2}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={2048}
          placeholder="Optional — what to do, what to look for."
        />
      </label>

      <div className="template-form-grid">
        <label className="field">
          <span>Suggested follow-up event</span>
          <select
            value={suggestedKind}
            onChange={(e) => setSuggestedKind(e.target.value as EventKind | "")}
          >
            <option value="">— none —</option>
            {EVENT_KINDS.map((k) => (
              <option key={k} value={k}>{k.replace(/_/g, " ")}</option>
            ))}
          </select>
        </label>

        <label className="field">
          <span>Sort order</span>
          <input
            type="number"
            step="1"
            inputMode="numeric"
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value)}
            className="template-sort"
          />
        </label>
      </div>

      {err && <p className="error">{err}</p>}

      <div className="form-actions">
        <button type="button" className="cancel-link" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="submit" disabled={busy}>
          {busy ? "Saving…" : isEdit ? "Save reminder" : "Add reminder"}
        </button>
      </div>
    </form>
  );
}
