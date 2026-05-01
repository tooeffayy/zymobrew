// Thin fetch wrapper around the Zymo HTTP API.
//
// Auth is cookie-based: /api/auth/login sets `zymo_session` (HttpOnly,
// SameSite=Lax) and the browser sends it back on every same-origin
// /api/* call. The wrapper sets `credentials: "same-origin"` explicitly
// so a future tightening of fetch defaults can't silently break sessions.
//
// Errors: any non-2xx is thrown as ApiError with the server's
// `{error: string}` message when present, falling back to the status
// text. Pages catch and render — no global error boundary yet.

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = "ApiError";
  }
}

// Body is `unknown` rather than a tighter Json union because typed
// payload shapes (e.g. RecipeFormPayload) don't structurally satisfy
// `Record<string, unknown>` — TS treats their optional fields as
// missing rather than `unknown`. We only ever JSON.stringify the body,
// so the runtime contract is "JSON-serializable".
async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : undefined,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 204) {
    return undefined as T;
  }

  // Read body once, then dispatch by status. Doing this in two steps
  // (instead of res.json() inside the !ok branch) means we can surface
  // a server error message even when the server set a misleading
  // content-type.
  const text = await res.text();
  const data = text ? safeJson(text) : undefined;

  if (!res.ok) {
    const msg = isErrorShape(data) ? data.error : res.statusText || "request failed";
    throw new ApiError(res.status, msg);
  }
  return data as T;
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function isErrorShape(v: unknown): v is { error: string } {
  return typeof v === "object" && v !== null && typeof (v as { error: unknown }).error === "string";
}

export const api = {
  get:    <T>(path: string)                 => request<T>("GET",    path),
  post:   <T>(path: string, body?: unknown) => request<T>("POST",   path, body),
  patch:  <T>(path: string, body?: unknown) => request<T>("PATCH",  path, body),
  delete: <T>(path: string)                 => request<T>("DELETE", path),
};

// --- Resource types -------------------------------------------------------

// Mirrors the AuthResponse / PublicUser shapes in openapi.yaml. Hand-typed
// for now; if these drift we'll generate from the spec.
export interface PublicUser {
  id: string;
  username: string;
  email: string;
  display_name?: string;
}

export interface AuthResponse {
  token: string;
  user: PublicUser;
}

export type BrewType = "mead" | "beer" | "cider" | "wine" | "kombucha";
export type Visibility = "public" | "unlisted" | "private";

// Mirrors RecipeListItem in openapi.yaml — kept in sync by hand for now.
export interface RecipeListItem {
  id: string;
  author_id: string;
  parent_id?: string;
  revision_count: number;
  fork_count: number;
  brew_type: BrewType;
  style?: string;
  name: string;
  description?: string;
  target_og?: number;
  target_fg?: number;
  target_abv?: number;
  batch_size_l?: number;
  visibility: Visibility;
  updated_at: string;
}

// Server uses `next_cursor: string | null` — null means end-of-list.
export interface RecipePage {
  recipes: RecipeListItem[];
  next_cursor: string | null;
}

export type IngredientKind =
  | "honey"
  | "water"
  | "yeast"
  | "nutrient"
  | "fruit"
  | "spice"
  | "oak"
  | "acid"
  | "tannin"
  | "other"
  | "juice"
  | "sugar";

export interface Ingredient {
  id: string;
  kind: IngredientKind;
  name: string;
  amount?: number;
  unit?: string;
  sort_order: number;
  details: Record<string, unknown>;
}

// --- Batch types ----------------------------------------------------------

// Stage enum values come from queries.BatchStage. The schema also has
// `archived` but we only surface the active lifecycle — archived
// batches won't appear in the list query today and the edit form
// doesn't expose it as an option.
export type BatchStage =
  | "planning"
  | "primary"
  | "secondary"
  | "aging"
  | "bottled"
  | "archived";

// Mirrors batchView in internal/server/batches.go.
export interface Batch {
  id: string;
  name: string;
  brew_type: BrewType;
  stage: BatchStage;
  started_at?: string;
  bottled_at?: string;
  visibility: Visibility;
  notes?: string;
  created_at: string;
  updated_at: string;
}

export interface BatchPage {
  batches: Batch[];
  next_cursor: string | null;
}

// Mirrors queries.EventKind. The order here is the order we surface
// in the kind selector — pitch/rack/bottle on top because they advance
// reminder anchors.
export type EventKind =
  | "pitch"
  | "rack"
  | "bottle"
  | "nutrient_addition"
  | "degas"
  | "addition"
  | "stabilize"
  | "backsweeten"
  | "photo"
  | "note";

export interface BatchEvent {
  id: string;
  batch_id: string;
  occurred_at: string;
  kind: EventKind;
  title?: string;
  description?: string;
  details: Record<string, unknown>;
}

export interface BatchEventPage {
  events: BatchEvent[];
}

// Mirrors readingView in internal/server/batches.go. At least one of
// gravity / temperature_c / ph is required at the API surface.
export interface Reading {
  id: string;
  batch_id: string;
  taken_at: string;
  gravity?: number;
  temperature_c?: number;
  ph?: number;
  notes?: string;
  source: string;
}

export interface ReadingPage {
  readings: Reading[];
}

// Mirrors tastingNoteView in internal/server/batches.go. Server enforces
// rating ∈ [1,5] and that at least one field is set.
export interface TastingNote {
  id: string;
  batch_id: string;
  author_id: string;
  tasted_at: string;
  rating?: number;
  aroma?: string;
  flavor?: string;
  mouthfeel?: string;
  finish?: string;
  notes?: string;
}

export interface TastingNotePage {
  tasting_notes: TastingNote[];
}

// Mirrors queries.ReminderStatus. `cancelled` is reachable via DELETE
// but reminders in that state come back filtered out of the active
// list, so the UI doesn't render them — kept here for completeness.
export type ReminderStatus =
  | "scheduled"
  | "fired"
  | "snoozed"
  | "completed"
  | "dismissed"
  | "cancelled";

export interface Reminder {
  id: string;
  batch_id?: string;
  title: string;
  description?: string;
  fire_at: string;
  status: ReminderStatus;
  fired_at?: string;
  completed_at?: string;
  suggested_event_kind?: EventKind;
  created_at: string;
}

// Mirrors the Recipe schema in openapi.yaml. Returned by GET /api/recipes/{id}
// — ingredients are the live (head-revision) set, not a revision snapshot.
export interface Recipe {
  id: string;
  author_id: string;
  parent_id?: string;
  revision_number: number;
  revision_count: number;
  fork_count: number;
  brew_type: BrewType;
  style?: string;
  name: string;
  description?: string;
  target_og?: number;
  target_fg?: number;
  target_abv?: number;
  batch_size_l?: number;
  visibility: Visibility;
  message?: string;
  ingredients: Ingredient[];
  created_at: string;
  updated_at: string;
}
