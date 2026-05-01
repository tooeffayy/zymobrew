// Web push client helpers — VAPID fetch, subscribe/unsubscribe, capability
// detection. Kept framework-free so the Notifications page (and any future
// push UI) can drive it without sharing state through React.
//
// Server contract:
//   GET  /api/push/public-key      → { public_key } | 404 (VAPID not configured)
//   POST /api/push/subscribe       → { id }
//   POST /api/push/unsubscribe     → 204
//
// Browser contract:
//   - Service worker registered at /sw.js (scope "/")
//   - Subscription created with userVisibleOnly: true (Chrome requires it)

import { ApiError, api } from "./api";

const SW_PATH = "/sw.js";

export type PushAvailability =
  // No Notification / PushManager / serviceWorker — nothing we can do.
  | { kind: "unsupported" }
  // VAPID keys not set on the server — operator must run `zymo vapid-keys`.
  | { kind: "not-configured" }
  // User explicitly denied permission. Sticky in most browsers — they have
  // to fix it in browser settings; we just surface the message.
  | { kind: "denied" }
  // Permission "default" or "granted" but no PushSubscription on this
  // browser. The "subscribe" button is enabled.
  | { kind: "not-subscribed"; vapidPublicKey: string }
  // Subscribed and registered with the server.
  | { kind: "subscribed"; vapidPublicKey: string; endpoint: string };

export function isPushSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}

// Resolve current state by combining server config + browser permission +
// existing subscription. Cheap to call on mount and after subscribe/unsubscribe.
export async function checkAvailability(): Promise<PushAvailability> {
  if (!isPushSupported()) return { kind: "unsupported" };

  let vapidPublicKey: string;
  try {
    const res = await api.get<{ public_key: string }>("/api/push/public-key");
    vapidPublicKey = res.public_key;
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      return { kind: "not-configured" };
    }
    throw e;
  }
  if (!vapidPublicKey) return { kind: "not-configured" };

  if (Notification.permission === "denied") return { kind: "denied" };

  // Look for an existing subscription on a registration we already have.
  // If the SW has never been registered we don't force-register here —
  // that would prompt for permission silently in some browsers. Wait
  // until the user clicks Subscribe.
  const reg = await navigator.serviceWorker.getRegistration(SW_PATH);
  if (reg) {
    const existing = await reg.pushManager.getSubscription();
    if (existing) {
      // The local PushSubscription survives events the server doesn't
      // see (account anonymization + re-register, backup restore older
      // than the subscription, fresh login on the same device). Without
      // this re-POST the UI would say "Subscribed" while push_devices
      // has no matching row and pushes silently no-op. The server
      // upserts on (user_id, token), so calling it on every mount is
      // safe — it just bumps last_seen_at when the row already exists.
      await reconcileSubscription(existing);
      return { kind: "subscribed", vapidPublicKey, endpoint: existing.endpoint };
    }
  }
  return { kind: "not-subscribed", vapidPublicKey };
}

async function reconcileSubscription(sub: PushSubscription): Promise<void> {
  const json = sub.toJSON() as {
    endpoint?: string;
    keys?: { p256dh?: string; auth?: string };
  };
  if (!json.endpoint || !json.keys?.p256dh || !json.keys?.auth) return;
  try {
    await api.post("/api/push/subscribe", {
      endpoint: json.endpoint,
      keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
    });
  } catch {
    // Best-effort: keep the optimistic "Subscribed" state. If the row
    // really is missing, the next reminder won't push, and the user can
    // unsubscribe + re-subscribe to recover.
  }
}

// Full subscribe path: ensure permission, register the SW, create a
// PushSubscription, register it server-side. Throws on any failure with
// a message suitable for surfacing in the UI.
export async function subscribe(vapidPublicKey: string): Promise<string> {
  if (!isPushSupported()) {
    throw new Error("This browser doesn't support web push.");
  }

  // Permission first. requestPermission() is a no-op if already granted,
  // and a hard refusal if already denied — both safe to call.
  const perm = await Notification.requestPermission();
  if (perm !== "granted") {
    throw new Error("Notification permission was not granted.");
  }

  // Register at scope "/" so the SW can receive pushes regardless of the
  // tab's current path. navigator.serviceWorker.register is idempotent —
  // re-registering the same script returns the existing registration.
  const reg = await navigator.serviceWorker.register(SW_PATH, { scope: "/" });
  await navigator.serviceWorker.ready;

  // If a stale subscription exists from a previous VAPID key it must be
  // dropped before re-subscribing — pushManager.subscribe rejects when an
  // active subscription is bound to a different applicationServerKey.
  const existing = await reg.pushManager.getSubscription();
  if (existing) {
    await existing.unsubscribe();
  }

  const sub = await reg.pushManager.subscribe({
    userVisibleOnly: true,
    applicationServerKey: urlBase64ToUint8Array(vapidPublicKey),
  });

  const json = sub.toJSON() as {
    endpoint?: string;
    keys?: { p256dh?: string; auth?: string };
  };
  if (!json.endpoint || !json.keys?.p256dh || !json.keys?.auth) {
    // Belt-and-suspenders — every browser that ships PushManager today
    // returns a complete object, but the spec leaves these optional.
    await sub.unsubscribe();
    throw new Error("Browser returned an incomplete push subscription.");
  }

  await api.post("/api/push/subscribe", {
    endpoint: json.endpoint,
    keys: { p256dh: json.keys.p256dh, auth: json.keys.auth },
  });
  return json.endpoint;
}

// Unsubscribe locally + server. Both sides are best-effort: if the
// browser side has already lost the subscription we still call the
// server with the stored endpoint so the row goes away.
export async function unsubscribe(endpoint: string): Promise<void> {
  if (isPushSupported()) {
    const reg = await navigator.serviceWorker.getRegistration(SW_PATH);
    if (reg) {
      const sub = await reg.pushManager.getSubscription();
      if (sub) {
        try { await sub.unsubscribe(); } catch { /* best effort */ }
      }
    }
  }
  try {
    await api.post("/api/push/unsubscribe", { endpoint });
  } catch (e) {
    // 404 = server already cleaned the row up (e.g. webpush 410 prune).
    // Anything else is worth surfacing.
    if (!(e instanceof ApiError) || e.status !== 404) throw e;
  }
}

// Convert a base64url-encoded VAPID public key (as the server returns it)
// into the Uint8Array PushManager.subscribe wants. The padding step is
// required because base64url drops trailing '='.
// Returns Uint8Array<ArrayBuffer> rather than Uint8Array<ArrayBufferLike>
// — PushSubscriptionOptions.applicationServerKey accepts BufferSource, and
// the new TS lib types treat the SharedArrayBuffer-backed default as
// incompatible. Allocating the ArrayBuffer first pins the type.
// Returns Uint8Array<ArrayBuffer> rather than the default
// Uint8Array<ArrayBufferLike>: PushSubscriptionOptions.applicationServerKey
// accepts BufferSource, and TS5.7+ treats the SharedArrayBuffer-backed
// default as incompatible. Pinning ArrayBuffer in the generic resolves it.
function urlBase64ToUint8Array(base64String: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
  const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(base64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}
