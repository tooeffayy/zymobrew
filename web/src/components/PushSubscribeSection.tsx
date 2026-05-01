import { useCallback, useEffect, useState } from "react";

import {
  PushAvailability,
  checkAvailability,
  subscribe,
  unsubscribe,
} from "../push";

// Per-browser web-push subscription. Lives on the Notifications page
// alongside the global delivery preferences. Two distinct knobs:
//
//   1. *This browser is registered* — managed here. One row per browser
//      in push_devices on the server.
//   2. *Push delivery is enabled* — managed in PrefsSection (PATCH
//      /api/notifications/prefs). A global mute across all of this
//      user's registered browsers.
//
// Both have to be on for a reminder to actually buzz the device.

export function PushSubscribeSection() {
  const [state, setState] = useState<PushAvailability | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoadError(null);
    try {
      const s = await checkAvailability();
      setState(s);
    } catch (e) {
      setLoadError(e instanceof Error ? e.message : "failed to check push status");
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const onSubscribe = async () => {
    if (!state || (state.kind !== "not-subscribed" && state.kind !== "subscribed")) return;
    setActionError(null);
    setBusy(true);
    try {
      await subscribe(state.vapidPublicKey);
      await refresh();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "subscribe failed");
    } finally {
      setBusy(false);
    }
  };

  const onUnsubscribe = async () => {
    if (!state || state.kind !== "subscribed") return;
    setActionError(null);
    setBusy(true);
    try {
      await unsubscribe(state.endpoint);
      await refresh();
    } catch (e) {
      setActionError(e instanceof Error ? e.message : "unsubscribe failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="recipe-section push-section">
      <h2>This browser</h2>
      {state === null && !loadError && <p className="muted">Checking…</p>}
      {loadError && <p className="error">{loadError}</p>}
      {state && <PushBody state={state} busy={busy} onSubscribe={onSubscribe} onUnsubscribe={onUnsubscribe} />}
      {actionError && <p className="error">{actionError}</p>}
    </section>
  );
}

function PushBody({
  state, busy, onSubscribe, onUnsubscribe,
}: {
  state: PushAvailability;
  busy: boolean;
  onSubscribe: () => void;
  onUnsubscribe: () => void;
}) {
  switch (state.kind) {
    case "unsupported":
      return (
        <div className="push-body">
          <p className="push-status push-status-off">Web push isn't supported here.</p>
          <p className="muted">
            Try a recent Chrome, Edge, or Firefox. Safari supports web push only when the SPA is added to the home screen / Dock.
          </p>
        </div>
      );

    case "not-configured":
      return (
        <div className="push-body">
          <p className="push-status push-status-off">Push isn't set up on this instance.</p>
          <p className="muted">
            The operator hasn't configured VAPID keys. Reminders still appear in the inbox above; they just won't pop up as system notifications.
          </p>
          <p className="muted">
            <small>To enable: run <code>zymo vapid-keys</code> and set the printed env vars.</small>
          </p>
        </div>
      );

    case "denied":
      return (
        <div className="push-body">
          <p className="push-status push-status-off">Notifications are blocked for this site.</p>
          <p className="muted">
            Browsers don't let pages re-prompt once denied. Open the lock / shield icon in the address bar, allow notifications, then reload.
          </p>
        </div>
      );

    case "not-subscribed":
      return (
        <div className="push-body">
          <p className="push-status push-status-off">Not subscribed on this browser.</p>
          <p className="muted">
            Subscribe to get system-level notifications when reminders fire — even when this tab is closed.
          </p>
          <div className="form-actions">
            <button type="button" onClick={onSubscribe} disabled={busy}>
              {busy ? "Subscribing…" : "Enable browser notifications"}
            </button>
          </div>
        </div>
      );

    case "subscribed":
      return (
        <div className="push-body">
          <p className="push-status push-status-on">Subscribed on this browser.</p>
          <p className="muted">
            Reminders will pop up as system notifications. The "Send push notifications" toggle below mutes every registered browser at once if you need a break.
          </p>
          <div className="form-actions">
            <button
              type="button"
              className="cancel-link"
              onClick={onUnsubscribe}
              disabled={busy}
            >
              {busy ? "Unsubscribing…" : "Unsubscribe this browser"}
            </button>
          </div>
        </div>
      );
  }
}
