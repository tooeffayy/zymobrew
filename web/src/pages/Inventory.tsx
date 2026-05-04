import {
  CSSProperties,
  FormEvent,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  Button as AriaButton,
  Cell,
  Checkbox,
  ComboBox,
  Column,
  Input as AriaInput,
  ListBox,
  ListBoxItem,
  Popover,
  Row,
  Selection,
  Table,
  TableBody,
  TableHeader,
} from "react-aria-components";

import {
  ApiError,
  IngredientKind,
  InventoryItem,
  InventoryListResponse,
  api,
} from "../api";
import { Modal } from "../components/Modal";

// Surfaced in this order in the kind selector and the table sort. Kept in
// sync with queries.IngredientKind on the server (the canonical source is
// the Postgres enum); see internal/server/inventory.go validation.
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

const KIND_RANK: Record<IngredientKind, number> = Object.fromEntries(
  KIND_ORDER.map((k, i) => [k, i]),
) as Record<IngredientKind, number>;

// Common units suggested in the unit ComboBox per ingredient kind. Order
// is "most-likely first" — what gets surfaced before the user types.
// `allowsCustomValue` on the ComboBox means anything not in this list is
// still accepted as free text, so brewing a habanero spice in "drops"
// (or whatever) keeps working without us having to anticipate it.
const UNIT_OPTIONS_BY_KIND: Record<IngredientKind, readonly string[]> = {
  honey: ["lb", "kg", "g", "oz"],
  sugar: ["lb", "kg", "g", "oz"],
  juice: ["gal", "L", "qt", "mL", "fl oz"],
  yeast: ["pack", "sachet", "g"],
  nutrient: ["g", "tsp", "tbsp"],
  fruit: ["lb", "kg", "g", "oz"],
  spice: ["g", "stick", "tsp", "tbsp"],
  oak: ["g", "oz", "stick", "spiral"],
  acid: ["g", "tsp"],
  tannin: ["g", "tsp"],
  water: ["gal", "L", "qt", "mL"],
  other: ["g", "mL", "ea"],
};

// The brewer's stockpile of ingredients. Per-user, free-text matching to
// recipes (no shared catalog yet). One react-aria Table with a Category
// column — sorting by (category, name) keeps related rows adjacent
// without splitting the list into per-kind sub-tables, which means the
// quantity column lines up across the entire inventory. Add/edit both
// route through the same modal, since react-aria-components Table
// requires every row to have exactly N cells (no colSpan), making
// in-place inline edit clumsy.
export function Inventory() {
  const [items, setItems] = useState<InventoryItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [editingItem, setEditingItem] = useState<InventoryItem | null>(null);
  const [selected, setSelected] = useState<Selection>(new Set());
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkError, setBulkError] = useState<string | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);

  const updateItem = useCallback((updated: InventoryItem) => {
    setItems((prev) => prev.map((i) => (i.id === updated.id ? updated : i)));
  }, []);

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

  // Server already orders kind asc, lower(name) asc, so this is a no-op
  // most of the time — but resort defensively to honour KIND_ORDER (the
  // server uses Postgres enum order, which matches but isn't load-bearing).
  const sorted = useMemo(() => {
    return [...items].sort((a, b) => {
      const ra = KIND_RANK[a.kind] ?? 99;
      const rb = KIND_RANK[b.kind] ?? 99;
      if (ra !== rb) return ra - rb;
      return a.name.toLowerCase().localeCompare(b.name.toLowerCase());
    });
  }, [items]);

  // Selection is a react-aria Selection (Set<Key> | "all"). For bulk
  // operations we materialise it into the actual id list, expanding "all"
  // to the visible (sorted) items.
  const selectedIDs = useMemo<string[]>(() => {
    if (selected === "all") return sorted.map((it) => it.id);
    return Array.from(selected, String);
  }, [selected, sorted]);

  // Content-measured width for the unit column. Chrome budget ≈ 48px:
  //   td padding (12px) + input padding (12px) + input border (2px)
  //   + combobox gap (2px) + chevron trigger (16px) + small safety buffer.
  // Min anchored at 7rem (112px) so the column doesn't collapse below the
  // "Unit" header label when the inventory is empty or all-short-units.
  const unitCandidates = useMemo(() => {
    const set = new Set<string>();
    for (const it of items) {
      if (it.unit) set.add(it.unit);
      for (const u of UNIT_OPTIONS_BY_KIND[it.kind] ?? []) set.add(u);
    }
    return [...set];
  }, [items]);

  const measureRef = useRef<HTMLSpanElement>(null);
  const [unitColWidth, setUnitColWidth] = useState<number>(112);
  useLayoutEffect(() => {
    const span = measureRef.current;
    if (!span) return;
    let max = 0;
    for (const s of unitCandidates) {
      span.textContent = s;
      const w = span.getBoundingClientRect().width;
      if (w > max) max = w;
    }
    setUnitColWidth(Math.max(112, Math.ceil(max + 48)));
  }, [unitCandidates]);

  const removeSelected = async () => {
    if (selectedIDs.length === 0) return;
    const noun = selectedIDs.length === 1 ? "item" : "items";
    if (!window.confirm(`Remove ${selectedIDs.length} ${noun} from inventory?`)) return;
    setBulkBusy(true);
    setBulkError(null);
    try {
      // Sequential — keeps the request volume sane and the failure mode
      // easy to reason about (first failure stops the loop, partials
      // surface via refetch). Inventory is bounded so latency is fine.
      for (const id of selectedIDs) {
        await api.delete(`/api/inventory/${encodeURIComponent(id)}`);
      }
      setSelected(new Set());
      await refetch();
    } catch (e) {
      setBulkError(e instanceof ApiError ? e.message : "remove failed");
      await refetch();
    } finally {
      setBulkBusy(false);
    }
  };

  if (loading) {
    return <div className="page"><p className="muted">Loading…</p></div>;
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

      <section className="recipe-section">
        <h2>Ingredients</h2>
        <div className="inventory-toolbar">
          <button
            type="button"
            className="action-button primary icon-button"
            onClick={() => setAddOpen(true)}
            aria-label="Add ingredient"
            title="Add ingredient"
          >
            <PlusIcon />
          </button>
          <button
            type="button"
            className="action-button icon-button"
            onClick={() => {
              const target = sorted.find((it) => it.id === selectedIDs[0]);
              if (target) setEditingItem(target);
            }}
            disabled={selectedIDs.length !== 1}
            aria-label="Edit selected ingredient"
            title={
              selectedIDs.length === 1
                ? "Edit selected ingredient"
                : "Select one ingredient to edit"
            }
          >
            <PencilIcon />
          </button>
          <button
            type="button"
            className="action-button danger icon-button"
            onClick={removeSelected}
            disabled={selectedIDs.length === 0 || bulkBusy}
            aria-label={
              selectedIDs.length > 0
                ? `Remove ${selectedIDs.length} selected`
                : "Remove selected"
            }
            title={
              selectedIDs.length > 0
                ? `Remove ${selectedIDs.length} selected`
                : "Select ingredients to remove"
            }
          >
            <TrashIcon />
          </button>
        </div>

        {loadError && <p className="error">{loadError}</p>}
        {bulkError && <p className="error">{bulkError}</p>}
        {rowError && <p className="error">{rowError}</p>}

        {items.length === 0 ? (
          <p className="muted inventory-empty">
            Nothing in your inventory yet. Add a few ingredients above and they'll
            show up alongside recipes that call for them.
          </p>
        ) : (
          <div
            className="ingredient-card inventory-card"
            style={{ "--inventory-unit-col-width": `${unitColWidth}px` } as CSSProperties}
            // react-aria's Table uses an onKeyDownCapture handler for
            // type-to-select navigation (typing 'k' jumps to the K-row,
            // stealing focus from a cell input). Capture handlers fire
            // outermost-first, so we swallow the offending keys here —
            // outside the Table — when target is one of our cell inputs.
            //
            // Only printable single characters trigger typeahead, so we
            // restrict stopPropagation to those. React's stopPropagation
            // also blocks the bubble phase, which would prevent our own
            // onKeyDown from firing — and we need bubble for Enter/Escape
            // (commit/revert). Native key events still drive the input's
            // controlled-value updates regardless, so typing keeps working.
            onKeyDownCapture={(e) => {
              const t = e.target as HTMLElement | null;
              if (!t?.classList.contains("inventory-cell-input")) return;
              if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
                e.stopPropagation();
              }
            }}
          >
            <Table
              aria-label="Inventory"
              className="inventory-table"
              selectionMode="multiple"
              selectedKeys={selected}
              onSelectionChange={setSelected}
            >
              <TableHeader>
                <Column className="inventory-col-select">
                  <Checkbox slot="selection" />
                </Column>
                <Column isRowHeader className="inventory-col-name">Name</Column>
                <Column className="inventory-col-category">Category</Column>
                <Column className="inventory-col-qty">Quantity</Column>
                <Column className="inventory-col-unit">Unit</Column>
              </TableHeader>
              <TableBody items={sorted}>
                {(it) => (
                  <Row className="inventory-row">
                    <Cell className="inventory-cell-select">
                      <Checkbox slot="selection" />
                    </Cell>
                    <Cell className="inventory-name">{it.name}</Cell>
                    <Cell className="inventory-category">{KIND_LABEL[it.kind]}</Cell>
                    <Cell className="inventory-qty">
                      <EditableQtyCell item={it} onUpdate={updateItem} onError={setRowError} />
                    </Cell>
                    <Cell className="inventory-unit">
                      <EditableUnitCell item={it} onUpdate={updateItem} onError={setRowError} />
                    </Cell>
                  </Row>
                )}
              </TableBody>
            </Table>
          </div>
        )}
        {/* Offscreen text-width probe for the unit column. Inherits the page
            font so its `getBoundingClientRect().width` matches what the cell
            input renders. visibility:hidden + position:absolute keeps it out
            of layout; aria-hidden hides it from assistive tech. */}
        <span
          ref={measureRef}
          aria-hidden
          className="inventory-unit-measure"
        />
      </section>

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

      <Modal
        isOpen={editingItem !== null}
        onOpenChange={(open) => { if (!open) setEditingItem(null); }}
        title={editingItem ? `Edit ${editingItem.name}` : "Edit"}
      >
        {(close) => editingItem && (
          <InventoryForm
            item={editingItem}
            onSaved={async () => {
              await refetch();
              setEditingItem(null);
              close();
            }}
            onDeleted={async () => {
              await refetch();
              setEditingItem(null);
              close();
            }}
            onCancel={() => { setEditingItem(null); close(); }}
          />
        )}
      </Modal>
    </div>
  );
}

// Shared add/edit form. When `item` is supplied we PATCH and surface a
// Remove button (DELETE); otherwise POST a new item. The form lives
// inside the Add or Edit modal — the Inventory table itself is read-only.
function InventoryForm({
  item, onSaved, onDeleted, onCancel,
}: {
  item?: InventoryItem;
  onSaved: () => Promise<void>;
  onDeleted?: () => Promise<void>;
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

  const onDelete = async () => {
    if (!item || !onDeleted) return;
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
    <form onSubmit={onSubmit} className="inventory-form">
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
      <input
        type="text"
        value={notes}
        onChange={(e) => setNotes(e.target.value)}
        placeholder="Notes (optional)"
        maxLength={2048}
        aria-label="Notes"
      />
      {err && <p className="error">{err}</p>}
      <div className="inventory-form-actions">
        {editing && onDeleted && (
          <button
            type="button"
            className="link-button event-delete inventory-form-remove"
            onClick={onDelete}
            disabled={busy}
          >
            Remove
          </button>
        )}
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

// Trim trailing zeros (1.500 → 1.5) capped at 3 decimals to match the
// server's NUMERIC(10,3) precision. Returns "" when amount is unknown so
// inline inputs render empty (with a "—" placeholder) instead of literal "—".
function fmtQtyInput(amount?: number): string {
  if (amount === undefined || amount === null) return "";
  return amount.toFixed(3).replace(/\.?0+$/, "");
}

function parseAmount(s: string): number | null {
  const t = s.trim();
  if (t === "") return null;
  const n = Number(t);
  return Number.isFinite(n) ? n : null;
}

// Inline-editable cells. Both commit on blur or Enter, revert on Escape,
// and update local state from the server-returned row so we don't refetch
// the whole inventory on every keystroke. Pointer events are stopped at
// the input boundary so clicking into the field doesn't toggle row
// selection in the surrounding react-aria Table.
//
// The PATCH endpoint COALESCEs missing fields, so clearing isn't supported.
// For amount that means a blank input reverts; for unit, an empty string
// is a real value (kind+name+unit is the recipe-match key) so we send it.
function EditableQtyCell({
  item, onUpdate, onError,
}: {
  item: InventoryItem;
  onUpdate: (it: InventoryItem) => void;
  onError: (msg: string | null) => void;
}) {
  const [value, setValue] = useState(() => fmtQtyInput(item.amount));
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setValue(fmtQtyInput(item.amount));
  }, [item.amount]);

  const commit = async () => {
    const parsed = parseAmount(value);
    if (parsed === null || parsed === item.amount) {
      setValue(fmtQtyInput(item.amount));
      return;
    }
    setBusy(true);
    onError(null);
    try {
      const updated = await api.patch<InventoryItem>(
        `/api/inventory/${encodeURIComponent(item.id)}`,
        { amount: parsed },
      );
      onUpdate(updated);
    } catch (e) {
      setValue(fmtQtyInput(item.amount));
      onError(e instanceof ApiError ? e.message : "save failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <input
      type="number"
      step="0.001"
      inputMode="decimal"
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onBlur={commit}
      // Stop every keystroke from bubbling to react-aria's Table/Row
      // handlers — printable keys would otherwise trigger type-to-select
      // (stealing focus to navigate rows) and Enter would toggle row
      // selection. We still handle Enter/Escape ourselves first.
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          (e.target as HTMLInputElement).blur();
        } else if (e.key === "Escape") {
          e.preventDefault();
          setValue(fmtQtyInput(item.amount));
          (e.target as HTMLInputElement).blur();
        }
        e.stopPropagation();
      }}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
      disabled={busy}
      placeholder="—"
      aria-label={`Quantity for ${item.name}`}
      className="inventory-cell-input inventory-qty-cell-input"
    />
  );
}

// ComboBox-backed unit field. The dropdown surfaces per-kind common
// units; allowsCustomValue keeps existing free-text values working.
// Commit semantics:
//   - typing + blur (Tab/click out) → commit
//   - Enter → blur → commit
//   - picking an option from the dropdown → commit immediately (the pick
//     is an explicit confirmation, no need to make the user also blur)
//   - Escape → revert to the server's current value, blur
//
// `inputValueRef` mirrors the latest input value so commit() always
// reads fresh — the onBlur handler installed on a render before
// selection captured the *old* inputValue in its closure, and was
// re-issuing a PATCH with the pre-selection value right after a pick
// committed the new one (silently reverting the server). The ref breaks
// that cycle. `lastCommittedRef` then dedupes redundant PATCHes when
// blur and selection both target the same value.
function EditableUnitCell({
  item, onUpdate, onError,
}: {
  item: InventoryItem;
  onUpdate: (it: InventoryItem) => void;
  onError: (msg: string | null) => void;
}) {
  const initial = item.unit ?? "";
  const [inputValue, setInputValueState] = useState(initial);
  const [busy, setBusy] = useState(false);
  const inputValueRef = useRef(initial);
  const lastCommittedRef = useRef(initial);

  const setInputValue = useCallback((v: string) => {
    inputValueRef.current = v;
    setInputValueState(v);
  }, []);

  useEffect(() => {
    const current = item.unit ?? "";
    lastCommittedRef.current = current;
    setInputValue(current);
  }, [item.unit, setInputValue]);

  const options = UNIT_OPTIONS_BY_KIND[item.kind] ?? [];

  const commit = useCallback(async () => {
    const trimmed = inputValueRef.current.trim();
    if (trimmed === lastCommittedRef.current) return;
    lastCommittedRef.current = trimmed;
    setBusy(true);
    onError(null);
    try {
      const updated = await api.patch<InventoryItem>(
        `/api/inventory/${encodeURIComponent(item.id)}`,
        { unit: trimmed },
      );
      onUpdate(updated);
    } catch (e) {
      const current = item.unit ?? "";
      setInputValue(current);
      lastCommittedRef.current = current;
      onError(e instanceof ApiError ? e.message : "save failed");
    } finally {
      setBusy(false);
    }
  }, [item.id, item.unit, onUpdate, onError, setInputValue]);

  return (
    <ComboBox
      aria-label={`Unit for ${item.name}`}
      inputValue={inputValue}
      onInputChange={setInputValue}
      onChange={(key) => {
        if (key !== null) {
          setInputValue(String(key));
          void commit();
        }
      }}
      allowsCustomValue
      menuTrigger="focus"
      isDisabled={busy}
      className="inventory-unit-combobox"
    >
      <AriaInput
        className="inventory-cell-input inventory-unit-cell-input"
        placeholder="—"
        maxLength={32}
        onBlur={() => void commit()}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            (e.target as HTMLInputElement).blur();
          } else if (e.key === "Escape") {
            e.preventDefault();
            const current = item.unit ?? "";
            setInputValue(current);
            lastCommittedRef.current = current;
            (e.target as HTMLInputElement).blur();
          }
        }}
        onPointerDown={(e) => e.stopPropagation()}
        onClick={(e) => e.stopPropagation()}
      />
      <AriaButton className="inventory-unit-trigger" aria-label="Show unit options">
        ▾
      </AriaButton>
      <Popover className="inventory-unit-popover" placement="bottom start">
        <ListBox className="inventory-unit-listbox">
          {options.map((u) => (
            <ListBoxItem key={u} id={u} className="inventory-unit-option">
              {u}
            </ListBoxItem>
          ))}
        </ListBox>
      </Popover>
    </ComboBox>
  );
}

function PlusIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M12 5v14" />
      <path d="M5 12h14" />
    </svg>
  );
}

function PencilIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
    </svg>
  );
}

function TrashIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d="M3 6h18" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      <path d="M10 11v6" />
      <path d="M14 11v6" />
    </svg>
  );
}
