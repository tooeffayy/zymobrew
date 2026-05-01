import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";

import { ApiError, Batch, BatchStage, BrewType, Recipe, Visibility } from "../api";
import { TimeInput } from "./TimeInput";

const BREW_TYPES: BrewType[] = ["mead", "cider", "wine"];
const VISIBILITIES: Visibility[] = ["public", "unlisted", "private"];

// Create defaults a batch to "planning"; the segmented control offers
// "primary" too for users backfilling a batch already in flight.
// Editing exposes the full lifecycle including bottled+archived.
const CREATE_STAGES: BatchStage[] = ["planning", "primary"];
const EDIT_STAGES: BatchStage[] = ["planning", "primary", "secondary", "aging", "bottled", "archived"];

const VISIBILITY_HELP: Record<Visibility, string> = {
  public:   "Visible on your profile.",
  unlisted: "Only people with the link.",
  private:  "Only you.",
};

export interface BatchFormPayload {
  name: string;
  brew_type?: BrewType;       // omitted on edit (server ignores it)
  stage: BatchStage;
  started_at?: string;        // RFC3339
  bottled_at?: string;        // RFC3339
  visibility: Visibility;
  notes?: string;
  recipe_id?: string;         // create only, when pinning to a recipe
}

interface BaseProps {
  submitLabel: string;
  submittingLabel: string;
  cancelTo: string;
  onSubmit: (payload: BatchFormPayload) => Promise<void>;
}

type Props =
  | (BaseProps & { mode: "create"; pinnedRecipe?: Recipe })
  | (BaseProps & { mode: "edit"; initial: Batch });

// Shared create/edit form. In edit mode brew_type is locked (server
// PATCH ignores it) and the bottled_at / archived stage become reachable.
// In create mode an optional pinned recipe locks brew_type to the
// recipe's type and surfaces a small confirmation card.
export function BatchForm(props: Props) {
  const isEdit = props.mode === "edit";
  const initial = isEdit ? props.initial : null;
  const pinned = props.mode === "create" ? props.pinnedRecipe : undefined;

  const defaultName = initial?.name ?? (pinned ? pinned.name : "");
  const defaultBrewType: BrewType =
    initial?.brew_type ?? pinned?.brew_type ?? "mead";

  const [name, setName] = useState(defaultName);
  const [brewType, setBrewType] = useState<BrewType>(defaultBrewType);
  const [stage, setStage] = useState<BatchStage>(initial?.stage ?? "planning");
  const [startedDate, setStartedDate] = useState(toDateInput(initial?.started_at));
  const [startedTime, setStartedTime] = useState(toTimeInput(initial?.started_at));
  const [bottledDate, setBottledDate] = useState(toDateInput(initial?.bottled_at));
  const [bottledTime, setBottledTime] = useState(toTimeInput(initial?.bottled_at));
  const [visibility, setVisibility] = useState<Visibility>(initial?.visibility ?? "private");
  const [notes, setNotes] = useState(initial?.notes ?? "");

  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const stageOptions = isEdit ? EDIT_STAGES : CREATE_STAGES;
  // Lock brew_type when a recipe is pinned: the server enforces a
  // private-recipe owner check on the underlying brew anyway, but the
  // batch-vs-recipe brew_type mismatch would just confuse users.
  const brewTypeLocked = isEdit || pinned !== undefined;

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const payload: BatchFormPayload = {
        name: name.trim(),
        stage,
        visibility,
      };
      if (!isEdit) payload.brew_type = brewType;
      const startedISO = combineDateTime(startedDate, startedTime);
      if (startedISO) payload.started_at = startedISO;
      if (isEdit) {
        const bottledISO = combineDateTime(bottledDate, bottledTime);
        if (bottledISO) payload.bottled_at = bottledISO;
      }
      if (notes.trim()) payload.notes = notes.trim();
      if (!isEdit && pinned) payload.recipe_id = pinned.id;

      await props.onSubmit(payload);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "submit failed");
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="recipe-form" noValidate>
      {pinned && !isEdit && (
        <section className="form-section pinned-recipe">
          <h2>Recipe</h2>
          <p className="pinned-recipe-body">
            Brewing from <Link to={`/recipes/${pinned.id}`}>{pinned.name}</Link>
            <span className="muted"> · revision {pinned.revision_number} of {pinned.revision_count}</span>
          </p>
          <p className="field-help muted">
            Reminders on this recipe will be scheduled when you set a start time.
          </p>
        </section>
      )}

      <section className="form-section">
        <h2>Basics</h2>

        <label className="field">
          <span>Name</span>
          <input
            type="text"
            required
            maxLength={200}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="e.g. Spring Orange Mead — batch 1"
          />
        </label>

        <div className="field">
          <span>Brew type</span>
          {brewTypeLocked ? (
            <span className={`pill pill-${brewType}`}>{brewType}</span>
          ) : (
            <Segmented
              options={BREW_TYPES}
              value={brewType}
              onChange={setBrewType}
              name="brew_type"
            />
          )}
          {isEdit && (
            <span className="field-help muted">Brew type is fixed once a batch starts.</span>
          )}
          {pinned && !isEdit && (
            <span className="field-help muted">Locked to the recipe's brew type.</span>
          )}
        </div>

        <div className="field">
          <span>Stage</span>
          <Segmented
            options={stageOptions}
            value={stage}
            onChange={setStage}
            name="stage"
          />
        </div>

        <div className="field">
          <span>Visibility</span>
          <Segmented
            options={VISIBILITIES}
            value={visibility}
            onChange={setVisibility}
            name="visibility"
          />
          <span className="field-help muted">{VISIBILITY_HELP[visibility]}</span>
        </div>
      </section>

      <section className="form-section">
        <h2>Timeline <span className="muted section-help">(optional)</span></h2>

        <DateTimeField
          label="Started at"
          date={startedDate}
          time={startedTime}
          onDateChange={setStartedDate}
          onTimeChange={setStartedTime}
          help={isEdit
            ? "Editing this re-anchors any pending reminders on this batch."
            : "Setting this on a recipe-pinned batch schedules its reminders."}
        />

        {isEdit && (
          <DateTimeField
            label="Bottled at"
            date={bottledDate}
            time={bottledTime}
            onDateChange={setBottledDate}
            onTimeChange={setBottledTime}
          />
        )}
      </section>

      <section className="form-section">
        <h2>Notes <span className="muted section-help">(optional)</span></h2>
        <label className="field">
          <span>Free-form notes</span>
          <textarea
            rows={4}
            maxLength={10240}
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            placeholder="Anything you want to remember about this batch."
          />
        </label>
      </section>

      {error && <p className="error">{error}</p>}

      <div className="form-actions">
        <button type="submit" disabled={submitting || name.trim() === ""}>
          {submitting ? props.submittingLabel : props.submitLabel}
        </button>
        <Link to={props.cancelTo} className="cancel-link">Cancel</Link>
      </div>
    </form>
  );
}

function Segmented<T extends string>({
  options, value, onChange, name,
}: {
  options: readonly T[];
  value: T;
  onChange: (v: T) => void;
  name: string;
}) {
  return (
    <div className="segmented" role="radiogroup" aria-label={name}>
      {options.map((opt) => (
        <label key={opt} className={`segmented-item${opt === value ? " active" : ""}`}>
          <input
            type="radio"
            name={name}
            value={opt}
            checked={opt === value}
            onChange={() => onChange(opt)}
          />
          <span>{opt}</span>
        </label>
      ))}
    </div>
  );
}

// Date+time pair with "Now" / "Clear" affordances. Most batches are
// started right now; the Now button reduces the common case to a
// single click, and the date picker is still there for backfills.
function DateTimeField({
  label, date, time, onDateChange, onTimeChange, help,
}: {
  label: string;
  date: string;
  time: string;
  onDateChange: (v: string) => void;
  onTimeChange: (v: string) => void;
  help?: string;
}) {
  const setNow = () => {
    const now = new Date().toISOString();
    onDateChange(toDateInput(now));
    onTimeChange(toTimeInput(now));
  };
  const clear = () => {
    onDateChange("");
    onTimeChange("");
  };
  const hasValue = date !== "" || time !== "";

  return (
    <div className="field datetime-field">
      <div className="datetime-field-head">
        <span>{label}</span>
        <span className="datetime-field-quick">
          <button type="button" className="link-button" onClick={setNow}>Now</button>
          {hasValue && (
            <button type="button" className="link-button" onClick={clear}>Clear</button>
          )}
        </span>
      </div>
      <div className="datetime-pair">
        <input
          type="date"
          value={date}
          onChange={(e) => onDateChange(e.target.value)}
          aria-label={`${label} date`}
        />
        <TimeInput
          value={time}
          onChange={onTimeChange}
          ariaLabel={`${label} time`}
        />
      </div>
      {help && <span className="field-help muted">{help}</span>}
    </div>
  );
}

// `<input type="date">` speaks "YYYY-MM-DD", `<input type="time">`
// speaks "HH:mm" — both in the local tz. We split (rather than use
// datetime-local) because the native datetime-local widget renders
// chunkier than two side-by-side fields.
function toDateInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

function toTimeInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// If date is missing we send nothing — the server treats started_at
// as null. If time is missing but date is set, midnight local; matches
// the native datetime-local convention for a date-only entry.
function combineDateTime(date: string, time: string): string | null {
  if (!date) return null;
  const t = time || "00:00";
  const d = new Date(`${date}T${t}`);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}
