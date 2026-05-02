import { FormEvent, useCallback, useEffect, useState } from "react";

import {
  ApiError,
  IngredientKind,
  InventoryItem,
  InventoryListResponse,
  api,
} from "../api";
import { Modal } from "../components/Modal";

// Surfaced in this order in the kind selector and the grouped list. Kept
// in sync with queries.IngredientKind on the server (the canonical source
// is the Postgres enum); see internal/server/inventory.go validation.
const KIND_ORDER: IngredientKind[] = [
  "honey", "sugar", "juice", "yeast", "nutrient",
  "fruit", "spice", "oak", "acid", "tannin",
  "water", "other",
];

const KIND_LABEL: Record<IngredientKind, string> = {
  honey: "Honey",
  water: "Water",
  yeast: "Yeast",
  nutrient: "Nutrient",
  fruit: "Fruit",
  spice: "Spice",
  oak: "Oak",
  acid: "Acid",
  tannin: "Tannin",
  other: "Other",
  juice: "Juice",
  sugar: "Sugar",
};

// The brewer's stockpile of ingredients. Per-user, free-text matching to
// recipes (no shared catalog yet). Surfaces here are intentionally lean —
// list grouped by kind, modal-add, inline-edit, single-row delete. The
// recipe-side integration that consumes this lives on RecipeDetail.
export function Inventory() {
  const [items, setItems] = useState<InventoryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [editingID, setEditingID] = useState<string | null>(null);

  const refetch = useCallback(async () => {
    try {
      const page = await api.get<InventoryListResponse>("/api/inventory");
      setItems(page.items);
      setLoadError(null);
    } catch (e) {
      setLoadError(e instanceof ApiError ? e.message : "failed to load inventory");
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    refetch().finally(() => setLoading(false));
  }, [refetch]);

  if (loading) {
    return <div className="page"><p className="muted">Loading…</p></div>;
  }

  // Group by kind for display. Server already orders by kind asc then
  // lower(name); we just bucket and skip empty kinds.
  const byKind = new Map<IngredientKind, InventoryItem[]>();
  for (const k of KIND_ORDER) byKind.set(k, []);
  for (const it of items) {
    const list = byKind.get(it.kind);
    if (list) list.push(it);
  }

  return (
    <div className="page">
      <header className="page-header">
        <div>
          <h1>Inventory</h1>
          <p className="recipe-detail-desc">
            What you currently have on hand. Recipes show have / short / missing
            badges based on this list — strict match on kind + name + unit.
          </p>
        </div>
      </header>

      <div className="inventory-toolbar">
        <button type="button" className="action-button primary" onClick={() => setAddOpen(true)}>
          + Add ingredient
        </button>
      </div>

      {loadError && <p className="error">{loadError}</p>}

      {items.length === 0 ? (
        <p className="muted inventory-empty">
          Nothing in your inventory yet. Add a few ingredients above and they'll
          show up alongside recipes that call for them.
        </p>
      ) : (
        <div className="ingredient-card inventory-card">
          {KIND_ORDER.map((k) => {
            const rows = byKind.get(k) ?? [];
            if (rows.length === 0) return null;
            return (
              <section key={k} className="ingredient-section">
                <h2 className="ingredient-kind">{KIND_LABEL[k]}</h2>
                {rows.map((it) =>
                  editingID === it.id ? (
                    <EditInventoryRow
                      key={it.id}
                      item={it}
                      onCancel={() => setEditingID(null)}
                      onSaved={async () => {
                        setEditingID(null);
                        await refetch();
                      }}
                      onDeleted={async () => {
                        setEditingID(null);
                        await refetch();
                      }}
                    />
                  ) : (
                    <InventoryRow
                      key={it.id}
                      item={it}
                      onEdit={() => setEditingID(it.id)}
                    />
                  ),
                )}
              </section>
            );
          })}
        </div>
      )}

      <Modal isOpen={addOpen} onOpenChange={setAddOpen} title="Add to inventory">
        {(close) => (
          <InventoryForm
            onSaved={async () => {
              await refetch();
              close();
            }}
          />
        )}
      </Modal>
    </div>
  );
}

function InventoryRow({ item, onEdit }: { item: InventoryItem; onEdit: () => void }) {
  return (
    <div className="ingredient-row inventory-row">
      <span className="ingredient-name">{item.name}</span>
      <span className="ingredient-amount">{fmtAmount(item.amount, item.unit)}</span>
      <button type="button" className="link-button inventory-edit-btn" onClick={onEdit}>
        Edit
      </button>
    </div>
  );
}

function EditInventoryRow({
  item, onCancel, onSaved, onDeleted,
}: {
  item: InventoryItem;
  onCancel: () => void;
  onSaved: () => Promise<void>;
  onDeleted: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onDelete = async () => {
    if (!window.confirm(`Remove "${item.name}" from inventory?`)) return;
    setBusy(true);
    setErr(null);
    try {
      await api.delete(`/api/inventory/${encodeURIComponent(item.id)}`);
      await onDeleted();
    } catch (e) {
      setErr(e instanceof ApiError ? e.message : "delete failed");
      setBusy(false);
    }
  };

  return (
    <div className="ingredient-row inventory-edit-row">
      <InventoryForm
        item={item}
        compact
        onSaved={onSaved}
        onCancel={onCancel}
      />
      <div className="inventory-edit-side">
        <button type="button" className="link-button event-delete" onClick={onDelete} disabled={busy}>
          {busy ? "Removing…" : "Remove"}
        </button>
        {err && <p className="error">{err}</p>}
      </div>
    </div>
  );
}

// Shared add/edit form. When `item` is supplied we PATCH; otherwise POST.
// `compact` strips the form-level wrapper padding for inline-edit usage.
function InventoryForm({
  item, compact, onSaved, onCancel,
}: {
  item?: InventoryItem;
  compact?: boolean;
  onSaved: () => Promise<void>;
  onCancel?: () => void;
}) {
  const editing = !!item;
  const [kind, setKind] = useState<IngredientKind>(item?.kind ?? "honey");
  const [name, setName] = useState(item?.name ?? "");
  const [amount, setAmount] = useState(
    typeof item?.amount === "number" ? String(item.amount) : "",
  );
  const [unit, setUnit] = useState(item?.unit ?? "");
  const [notes, setNotes] = useState(item?.notes ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setErr(null);
    setBusy(true);
    try {
      const body: Record<string, unknown> = { kind, name: name.trim() };
      const amt = parseAmount(amount);
      if (amt !== null) body.amount = amt;
      const trimmedUnit = unit.trim();
      if (editing || trimmedUnit) body.unit = trimmedUnit;
      const trimmedNotes = notes.trim();
      if (editing || trimmedNotes) body.notes = trimmedNotes;
      if (editing && item) {
        await api.patch(`/api/inventory/${encodeURIComponent(item.id)}`, body);
      } else {
        await api.post("/api/inventory", body);
      }
      await onSaved();
    } catch (e2) {
      setErr(e2 instanceof ApiError ? e2.message : "save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      onSubmit={onSubmit}
      className={`inventory-form${compact ? " inventory-form-compact" : ""}`}
    >
      <div className="inventory-form-row">
        <select
          value={kind}
          onChange={(e) => setKind(e.target.value as IngredientKind)}
          aria-label="Ingredient kind"
        >
          {KIND_ORDER.map((k) => (
            <option key={k} value={k}>{KIND_LABEL[k]}</option>
          ))}
        </select>
        <input
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Name (e.g. Wildflower honey)"
          maxLength={200}
          required
          aria-label="Name"
        />
      </div>
      <div className="inventory-form-row">
        <input
          type="number"
          step="0.001"
          inputMode="decimal"
          value={amount}
          onChange={(e) => setAmount(e.target.value)}
          placeholder="Amount"
          aria-label="Amount"
          className="inventory-amount-input"
        />
        <input
          type="text"
          value={unit}
          onChange={(e) => setUnit(e.target.value)}
          placeholder="Unit (g, lb, mL…)"
          maxLength={32}
          aria-label="Unit"
          className="inventory-unit-input"
        />
      </div>
      {!compact && (
        <input
          type="text"
          value={notes}
          onChange={(e) => setNotes(e.target.value)}
          placeholder="Notes (optional)"
          maxLength={2048}
          aria-label="Notes"
        />
      )}
      {err && <p className="error">{err}</p>}
      <div className="inventory-form-actions">
        {onCancel && (
          <button type="button" className="cancel-link" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
        )}
        <button type="submit" disabled={busy || !name.trim()}>
          {busy ? "Saving…" : editing ? "Save" : "Add to inventory"}
        </button>
      </div>
    </form>
  );
}

function fmtAmount(amount?: number, unit?: string): string {
  if (amount === undefined || amount === null) {
    return unit ? `— ${unit}` : "—";
  }
  // Trim trailing zeros (1.500 → 1.5) but cap at 3 decimals; matches the
  // server's NUMERIC(10,3) precision.
  const fixed = amount.toFixed(3).replace(/\.?0+$/, "");
  return unit ? `${fixed} ${unit}` : fixed;
}

function parseAmount(s: string): number | null {
  const t = s.trim();
  if (t === "") return null;
  const n = Number(t);
  return Number.isFinite(n) ? n : null;
}
