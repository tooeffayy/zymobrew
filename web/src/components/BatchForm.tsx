import { FormEvent, useState } from "react";
import { Link } from "react-router-dom";

import { ApiError, Batch, BatchStage, BrewType, Recipe, Visibility } from "../api";

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
  const [startedAt, setStartedAt] = useState(toLocalInput(initial?.started_at));
  const [bottledAt, setBottledAt] = useState(toLocalInput(initial?.bottled_at));
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
      const startedISO = fromLocalInput(startedAt);
      if (startedISO) payload.started_at = startedISO;
      if (isEdit) {
        const bottledISO = fromLocalInput(bottledAt);
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

        <label className="field">
          <span>Started at</span>
          <input
            type="datetime-local"
            value={startedAt}
            onChange={(e) => setStartedAt(e.target.value)}
          />
          <span className="field-help muted">
            {isEdit
              ? "Editing this re-anchors any pending reminders on this batch."
              : "Setting this on a recipe-pinned batch schedules its reminders."}
          </span>
        </label>

        {isEdit && (
          <label className="field">
            <span>Bottled at</span>
            <input
              type="datetime-local"
              value={bottledAt}
              onChange={(e) => setBottledAt(e.target.value)}
            />
          </label>
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

// datetime-local <-> RFC3339. The native control speaks "YYYY-MM-DDTHH:mm"
// in the local tz; we send RFC3339 with the offset baked in so the server
// stores wall-clock unambiguously.
function toLocalInput(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  // Pull local components — `toISOString` would yield UTC, which would
  // be off by hours when displayed in the input.
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(local: string): string | null {
  if (!local) return null;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? null : d.toISOString();
}
