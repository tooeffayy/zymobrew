import { FormEvent, useEffect, useMemo, useState } from "react";
import {
  Input as AriaInput,
  ComboBox,
  ListBox,
  ListBoxItem,
  Popover,
} from "react-aria-components";
import { Link } from "react-router-dom";

import {
  ApiError,
  BrewType,
  Ingredient,
  IngredientKind,
  InventoryItem,
  InventoryListResponse,
  Recipe,
  Visibility,
  api,
} from "../api";

// API surface only accepts mead/cider/wine today (server's
// allowedBrewTypes); beer/kombucha land in later phases.
const BREW_TYPES: BrewType[] = ["mead", "cider", "wine"];
const VISIBILITIES: Visibility[] = ["public", "unlisted", "private"];

// Same canonical brewing order used by the detail-page list — keeps
// honey/juice/sugar near the top so adding fermentables first feels
// natural in a new-recipe flow.
const INGREDIENT_KINDS: IngredientKind[] = [
  "honey", "juice", "sugar", "fruit",
  "yeast", "nutrient",
  "water", "acid", "tannin", "spice", "oak", "other",
];

// Per-kind unit suggestions. Order within each group is most-common-first.
// `unit` on the wire is still a free string — the API doesn't constrain
// it. The empty option ("Unit") stays available for ingredients that
// shouldn't be quantified. `other` collects the union for one-off cases.
const KIND_UNIT_GROUPS: Record<IngredientKind, { label: string; units: string[] }[]> = {
  honey:    [{ label: "Mass",   units: ["kg", "g", "lb", "oz"] }],
  juice:    [{ label: "Volume", units: ["L", "mL", "gal", "qt", "pt", "fl oz", "cup"] }],
  sugar:    [{ label: "Mass",   units: ["g", "kg", "oz", "lb"] }],
  fruit:    [
    { label: "Mass",  units: ["g", "kg", "oz", "lb"] },
    { label: "Count", units: ["each"] },
  ],
  yeast:    [
    { label: "Count", units: ["pack", "each"] },
    { label: "Mass",  units: ["g"] },
  ],
  nutrient: [
    { label: "Mass",   units: ["g", "oz"] },
    { label: "Volume", units: ["tsp"] },
  ],
  water:    [{ label: "Volume", units: ["L", "mL", "gal", "qt", "cup"] }],
  acid:     [
    { label: "Mass",   units: ["g", "oz"] },
    { label: "Volume", units: ["mL", "tsp", "tbsp"] },
  ],
  tannin:   [
    { label: "Mass",   units: ["g", "oz"] },
    { label: "Volume", units: ["tsp", "tbsp"] },
  ],
  spice:    [
    { label: "Mass",   units: ["g", "oz"] },
    { label: "Volume", units: ["tsp", "tbsp"] },
    { label: "Count",  units: ["each"] },
  ],
  oak:      [{ label: "Mass", units: ["g", "oz"] }],
  other:    [
    { label: "Mass",   units: ["g", "kg", "oz", "lb"] },
    { label: "Volume", units: ["mL", "L", "tsp", "tbsp", "cup", "fl oz", "pt", "qt", "gal"] },
    { label: "Count",  units: ["pack", "each"] },
  ],
};

function unitsForKind(kind: IngredientKind): string[] {
  return KIND_UNIT_GROUPS[kind].flatMap((g) => g.units);
}

const VISIBILITY_HELP: Record<Visibility, string> = {
  public:   "Anyone can find it in the feed.",
  unlisted: "Only people with the link.",
  private:  "Only you.",
};

interface IngredientDraft {
  // Stable client-side key so React keys don't shift when reordering.
  // Not sent to the server — sort_order is derived from array index.
  uid: string;
  kind: IngredientKind;
  name: string;
  amount: string;
  unit: string;
}

function newDraft(kind: IngredientKind = "honey"): IngredientDraft {
  return { uid: crypto.randomUUID(), kind, name: "", amount: "", unit: "" };
}

function draftFromIngredient(ing: Ingredient): IngredientDraft {
  return {
    uid: crypto.randomUUID(),
    kind: ing.kind,
    name: ing.name,
    amount: ing.amount != null ? String(ing.amount) : "",
    unit: ing.unit ?? "",
  };
}

// Wire shape the form produces. Pages decide POST vs PATCH and what to
// do with the result.
export interface RecipeFormPayload {
  name: string;
  brew_type?: BrewType;            // omitted on edit (server ignores it)
  style?: string;
  description?: string;
  visibility: Visibility;
  target_og?: number;
  target_fg?: number;
  target_abv?: number;
  batch_size_l?: number;
  message?: string;
  ingredients: {
    kind: IngredientKind;
    name: string;
    amount?: number;
    unit?: string;
    sort_order: number;
  }[];
}

interface BaseProps {
  submitLabel: string;
  submittingLabel: string;
  cancelTo: string;
  onSubmit: (payload: RecipeFormPayload) => Promise<void>;
}

type Props =
  | (BaseProps & { mode: "create" })
  | (BaseProps & { mode: "edit"; initial: Recipe });

// Shared create/edit form. In edit mode brew_type is locked (server
// PATCH doesn't accept it) and a "What changed?" message field appears
// — that becomes the revision summary.
export function RecipeForm(props: Props) {
  const isEdit = props.mode === "edit";
  const initial = isEdit ? props.initial : null;

  const [name, setName] = useState(initial?.name ?? "");
  const [brewType, setBrewType] = useState<BrewType>(initial?.brew_type ?? "mead");
  const [style, setStyle] = useState(initial?.style ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [visibility, setVisibility] = useState<Visibility>(initial?.visibility ?? "public");
  const [targetOG, setTargetOG] = useState(numToStr(initial?.target_og));
  const [targetFG, setTargetFG] = useState(numToStr(initial?.target_fg));
  const [targetABV, setTargetABV] = useState(numToStr(initial?.target_abv));
  const [batchSizeL, setBatchSizeL] = useState(numToStr(initial?.batch_size_l));
  const [message, setMessage] = useState("");
  const [ingredients, setIngredients] = useState<IngredientDraft[]>(
    initial && initial.ingredients.length > 0
      ? [...initial.ingredients]
          .sort((a, b) => a.sort_order - b.sort_order)
          .map(draftFromIngredient)
      : [newDraft()],
  );

  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Inventory drives the name-field autocomplete. Best-effort: a 401/network
  // failure just means the user types names freehand. Fetched once per mount.
  const [inventory, setInventory] = useState<InventoryItem[]>([]);
  useEffect(() => {
    let cancelled = false;
    api
      .get<InventoryListResponse>("/api/inventory")
      .then((res) => { if (!cancelled) setInventory(res.items); })
      .catch(() => { /* silent — autocomplete is enhancement-only */ });
    return () => { cancelled = true; };
  }, []);

  const updateIngredient = (uid: string, patch: Partial<IngredientDraft>) =>
    setIngredients((prev) =>
      prev.map((ing) => {
        if (ing.uid !== uid) return ing;
        const next = { ...ing, ...patch };
        // When the kind changes, drop the unit if it isn't valid for
        // the new kind — otherwise switching spice → honey would leave
        // "tsp" hanging in the field.
        if (patch.kind !== undefined && patch.kind !== ing.kind) {
          if (next.unit && !unitsForKind(next.kind).includes(next.unit)) {
            next.unit = "";
          }
        }
        return next;
      }),
    );

  const addIngredient = () => setIngredients((prev) => [...prev, newDraft()]);

  const removeIngredient = (uid: string) =>
    setIngredients((prev) => prev.filter((ing) => ing.uid !== uid));

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const payload: RecipeFormPayload = {
        name: name.trim(),
        visibility,
        // Skip blank rows so a half-filled ingredient list doesn't 400.
        // sort_order = index keeps the order the user sees.
        ingredients: ingredients
          .filter((ing) => ing.name.trim() !== "")
          .map((ing, idx) => {
            const out: RecipeFormPayload["ingredients"][number] = {
              kind: ing.kind,
              name: ing.name.trim(),
              sort_order: idx,
            };
            const amt = parseNum(ing.amount);
            if (amt != null) out.amount = amt;
            if (ing.unit.trim()) out.unit = ing.unit.trim();
            return out;
          }),
      };
      if (!isEdit) payload.brew_type = brewType;
      if (style.trim()) payload.style = style.trim();
      if (description.trim()) payload.description = description.trim();
      const og = parseNum(targetOG);
      const fg = parseNum(targetFG);
      const abv = parseNum(targetABV);
      const bs = parseNum(batchSizeL);
      if (og != null) payload.target_og = og;
      if (fg != null) payload.target_fg = fg;
      if (abv != null) payload.target_abv = abv;
      if (bs != null) payload.batch_size_l = bs;
      if (isEdit && message.trim()) payload.message = message.trim();

      await props.onSubmit(payload);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "submit failed");
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={onSubmit} className="recipe-form" noValidate>
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
            placeholder="e.g. Orange Blossom Mead"
          />
        </label>

        <div className="field">
          <span>Brew type</span>
          {isEdit ? (
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
            <span className="field-help muted">Brew type is fixed once a recipe is created.</span>
          )}
        </div>

        <label className="field">
          <span>Style <span className="muted">(optional)</span></span>
          <input
            type="text"
            maxLength={100}
            value={style}
            onChange={(e) => setStyle(e.target.value)}
            placeholder="e.g. Traditional, Sack, Dry English"
          />
        </label>

        <label className="field">
          <span>Description <span className="muted">(optional)</span></span>
          <textarea
            rows={3}
            maxLength={10240}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="Notes on the recipe — what you're going for, where it came from."
          />
        </label>

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
        <h2>Targets <span className="muted section-help">(all optional)</span></h2>
        <div className="targets-grid">
          <NumberField
            label="OG"
            hint="0.990–1.200"
            step="0.001"
            value={targetOG}
            onChange={setTargetOG}
          />
          <NumberField
            label="FG"
            hint="0.990–1.200"
            step="0.001"
            value={targetFG}
            onChange={setTargetFG}
          />
          <NumberField
            label="ABV"
            hint="percent"
            step="0.1"
            value={targetABV}
            onChange={setTargetABV}
          />
          <NumberField
            label="Batch"
            hint="liters"
            step="0.5"
            value={batchSizeL}
            onChange={setBatchSizeL}
          />
        </div>
      </section>

      <section className="form-section">
        <h2>Ingredients <span className="muted section-help">(up to 50)</span></h2>
        <div className="ingredient-edit-list">
          {ingredients.map((ing) => (
            <IngredientRow
              key={ing.uid}
              draft={ing}
              inventory={inventory}
              onChange={(patch) => updateIngredient(ing.uid, patch)}
              onRemove={ingredients.length > 1 ? () => removeIngredient(ing.uid) : undefined}
            />
          ))}
        </div>
        <button
          type="button"
          className="add-button"
          onClick={addIngredient}
          disabled={ingredients.length >= 50}
        >
          + Add ingredient
        </button>
      </section>

      {isEdit && (
        <section className="form-section">
          <h2>Revision note <span className="muted section-help">(optional)</span></h2>
          <label className="field">
            <span>What changed?</span>
            <input
              type="text"
              maxLength={500}
              value={message}
              onChange={(e) => setMessage(e.target.value)}
              placeholder="e.g. Bumped honey to 1.5 kg, swapped to D47"
            />
            <span className="field-help muted">Saved with the revision history.</span>
          </label>
        </section>
      )}

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

function NumberField({
  label, hint, value, onChange, step,
}: {
  label: string;
  hint: string;
  value: string;
  onChange: (v: string) => void;
  step: string;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      <input
        type="number"
        inputMode="decimal"
        step={step}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={hint}
      />
    </label>
  );
}

function IngredientRow({
  draft, inventory, onChange, onRemove,
}: {
  draft: IngredientDraft;
  inventory: InventoryItem[];
  onChange: (patch: Partial<IngredientDraft>) => void;
  onRemove?: () => void;
}) {
  // Filter to the current kind so a "honey" row only suggests honeys.
  // ComboBox's built-in filter then narrows by what the user types.
  const inventoryForKind = useMemo(
    () => inventory.filter((it) => it.kind === draft.kind),
    [inventory, draft.kind],
  );

  const onPickInventory = (key: React.Key | null) => {
    if (key === null) return;
    const item = inventoryForKind.find((it) => it.id === String(key));
    if (!item) return;
    const patch: Partial<IngredientDraft> = { name: item.name };
    // Convenience: prefill the unit if the row has none yet and the
    // inventory unit is one of the kind's known options.
    if (!draft.unit.trim() && item.unit && unitsForKind(draft.kind).includes(item.unit)) {
      patch.unit = item.unit;
    }
    onChange(patch);
  };

  return (
    <div className="ingredient-edit-row">
      <select
        className="kind-select"
        value={draft.kind}
        onChange={(e) => onChange({ kind: e.target.value as IngredientKind })}
        aria-label="Kind"
      >
        {INGREDIENT_KINDS.map((k) => (
          <option key={k} value={k}>{k}</option>
        ))}
      </select>
      <ComboBox
        aria-label="Name"
        inputValue={draft.name}
        onInputChange={(value) => onChange({ name: value })}
        onSelectionChange={onPickInventory}
        allowsCustomValue
        menuTrigger="input"
        className="ingredient-name-combobox"
      >
        <AriaInput
          className="ingredient-name-input"
          placeholder="Name"
          maxLength={200}
        />
        <Popover className="ingredient-name-popover" placement="bottom start">
          <ListBox className="ingredient-name-listbox" items={inventoryForKind}>
            {(it) => (
              <ListBoxItem
                id={it.id}
                className="ingredient-name-option"
                textValue={it.name}
              >
                <span className="ingredient-name-option-name">{it.name}</span>
                {(it.amount != null || it.unit) && (
                  <span className="ingredient-name-option-meta muted">
                    {it.amount != null ? it.amount : ""}
                    {it.amount != null && it.unit ? " " : ""}
                    {it.unit ?? ""}
                  </span>
                )}
              </ListBoxItem>
            )}
          </ListBox>
        </Popover>
      </ComboBox>
      <input
        type="number"
        inputMode="decimal"
        step="any"
        value={draft.amount}
        onChange={(e) => onChange({ amount: e.target.value })}
        placeholder="Amount"
        aria-label="Amount"
      />
      <select
        className="unit-select"
        value={draft.unit}
        onChange={(e) => onChange({ unit: e.target.value })}
        aria-label="Unit"
      >
        <option value="">Unit</option>
        {KIND_UNIT_GROUPS[draft.kind].map((g) => (
          <optgroup key={g.label} label={g.label}>
            {g.units.map((u) => (
              <option key={u} value={u}>{u}</option>
            ))}
          </optgroup>
        ))}
      </select>
      <button
        type="button"
        className="row-remove"
        onClick={onRemove}
        disabled={!onRemove}
        aria-label="Remove ingredient"
        title="Remove"
      >
        ×
      </button>
    </div>
  );
}

function parseNum(s: string): number | null {
  const t = s.trim();
  if (t === "") return null;
  const n = Number(t);
  return Number.isFinite(n) ? n : null;
}

function numToStr(n: number | undefined): string {
  return n == null ? "" : String(n);
}
