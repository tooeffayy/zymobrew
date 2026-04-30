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

type Json = Record<string, unknown> | unknown[] | string | number | boolean | null;

async function request<T>(method: string, path: string, body?: Json): Promise<T> {
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
  get:    <T>(path: string)              => request<T>("GET",    path),
  post:   <T>(path: string, body?: Json) => request<T>("POST",   path, body),
  patch:  <T>(path: string, body?: Json) => request<T>("PATCH",  path, body),
  delete: <T>(path: string)              => request<T>("DELETE", path),
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
