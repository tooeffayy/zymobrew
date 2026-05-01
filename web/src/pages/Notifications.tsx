import { FormEvent, useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import {
  ApiError,
  Notification,
  NotificationPage,
  NotificationPrefs,
  api,
} from "../api";
import { PushSubscribeSection } from "../components/PushSubscribeSection";
import { useNotifications } from "../notifications";

// In-app notification inbox + delivery preferences. The inbox here
// paginates independently of the global NotificationsProvider — that
// provider only holds page 1 to power the header badge.
export function Notifications() {
  const { refresh: refreshGlobal } = useNotifications();

  const [items, setItems] = useState<Notification[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const loadFirstPage = useCallback(async () => {
    setError(null);
    try {
      const page = await api.get<NotificationPage>("/api/notifications?limit=50");
      setItems(page.notifications);
      setNextCursor(page.next_cursor);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to load notifications");
    }
  }, []);

  useEffect(() => {
    setLoading(true);
    loadFirstPage().finally(() => setLoading(false));
  }, [loadFirstPage]);

  const loadMore = async () => {
    if (!nextCursor || loadingMore) return;
    setLoadingMore(true);
    try {
      const page = await api.get<NotificationPage>(
        `/api/notifications?limit=50&cursor=${encodeURIComponent(nextCursor)}`,
      );
      setItems((prev) => [...prev, ...page.notifications]);
      setNextCursor(page.next_cursor);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to load more");
    } finally {
      setLoadingMore(false);
    }
  };

  const markRead = async (id: string) => {
    setBusy(true);
    try {
      await api.post(`/api/notifications/${encodeURIComponent(id)}/read`);
      // Patch in place — full refetch would jolt the scroll position
      // and lose any "load more" pages already in view.
      setItems((prev) =>
        prev.map((n) => (n.id === id && !n.read_at ? { ...n, read_at: new Date().toISOString() } : n)),
      );
      void refreshGlobal();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to mark read");
    } finally {
      setBusy(false);
    }
  };

  const markAllRead = async () => {
    setBusy(true);
    try {
      await api.post("/api/notifications/read-all");
      const stamp = new Date().toISOString();
      setItems((prev) => prev.map((n) => (n.read_at ? n : { ...n, read_at: stamp })));
      void refreshGlobal();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to mark all read");
    } finally {
      setBusy(false);
    }
  };

  const unread = items.reduce((acc, n) => (n.read_at ? acc : acc + 1), 0);

  return (
    <div className="page">
      <header className="notifications-header">
        <h1>Notifications</h1>
        {unread > 0 && (
          <button
            type="button"
            className="action-button"
            onClick={markAllRead}
            disabled={busy}
          >
            Mark all read
          </button>
        )}
      </header>

      {error && <p className="error">{error}</p>}

      {loading ? (
        <p className="muted">Loading…</p>
      ) : items.length === 0 ? (
        <p className="muted">No notifications yet — set reminders on a batch and they'll show up here when they fire.</p>
      ) : (
        <>
          <ul className="notification-list">
            {items.map((n) => (
              <NotificationRow key={n.id} n={n} busy={busy} onMarkRead={() => markRead(n.id)} />
            ))}
          </ul>
          {nextCursor && (
            <div className="notifications-load-more">
              <button
                type="button"
                className="action-button"
                onClick={loadMore}
                disabled={loadingMore}
              >
                {loadingMore ? "Loading…" : "Load more"}
              </button>
            </div>
          )}
        </>
      )}

      <PushSubscribeSection />
      <PrefsSection />
    </div>
  );
}

function NotificationRow({
  n, busy, onMarkRead,
}: {
  n: Notification;
  busy: boolean;
  onMarkRead: () => void;
}) {
  const unread = !n.read_at;
  return (
    <li className={`notification-row${unread ? " notification-unread" : ""}`}>
      <div className="notification-main">
        <div className="notification-head">
          <span className="notification-title">{n.title}</span>
          <span className="muted notification-when" title={fmtDateTime(n.created_at)}>
            {fmtRelative(n.created_at)}
          </span>
        </div>
        {n.body && <p className="notification-body">{n.body}</p>}
        {n.url_path && (
          <p className="notification-link">
            <Link to={n.url_path}>View →</Link>
          </p>
        )}
      </div>
      {unread && (
        <div className="notification-actions">
          <button type="button" className="link-button" onClick={onMarkRead} disabled={busy}>
            Mark read
          </button>
        </div>
      )}
    </li>
  );
}

// --- Preferences ---------------------------------------------------------

function PrefsSection() {
  const [prefs, setPrefs] = useState<NotificationPrefs | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState<string | null>(null);

  // Form-local mirrors of the server fields. Initialized from `prefs`
  // once it loads; thereafter the user owns them until they save.
  const [pushEnabled, setPushEnabled]   = useState(true);
  const [emailEnabled, setEmailEnabled] = useState(false);
  const [quietStart, setQuietStart]     = useState("");
  const [quietEnd, setQuietEnd]         = useState("");
  const [timezone, setTimezone]         = useState("");

  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    setLoading(true);
    api
      .get<NotificationPrefs>("/api/notifications/prefs")
      .then((p) => {
        setPrefs(p);
        setPushEnabled(p.push_enabled);
        setEmailEnabled(p.email_enabled);
        setQuietStart(p.quiet_hours_start ?? "");
        setQuietEnd(p.quiet_hours_end ?? "");
        // If the server's value is the bare default ("UTC") and the
        // browser knows a real timezone, prefill that — saves the user
        // typing it on first save. They can still change it.
        const fallback =
          p.timezone === "UTC"
            ? Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"
            : p.timezone;
        setTimezone(fallback);
      })
      .catch((e) => setLoadError(e instanceof ApiError ? e.message : "failed to load preferences"))
      .finally(() => setLoading(false));
  }, []);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setSaveError(null);
    setSaved(false);
    // Quiet-hours pairing: server accepts either set independently,
    // but a half-set window is meaningless. Enforce here.
    if ((quietStart && !quietEnd) || (!quietStart && quietEnd)) {
      setSaveError("Set both quiet-hour times, or clear both.");
      return;
    }
    setSaving(true);
    try {
      // Send everything every time. The server's COALESCE pattern means
      // omitting a field leaves the prior value — but we want the form
      // to be the source of truth on submit.
      const body: Record<string, unknown> = {
        push_enabled: pushEnabled,
        email_enabled: emailEnabled,
        timezone: timezone || "UTC",
      };
      if (quietStart) body.quiet_hours_start = quietStart;
      if (quietEnd)   body.quiet_hours_end   = quietEnd;
      const updated = await api.patch<NotificationPrefs>("/api/notifications/prefs", body);
      setPrefs(updated);
      setSaved(true);
    } catch (e) {
      setSaveError(e instanceof ApiError ? e.message : "save failed");
    } finally {
      setSaving(false);
    }
  };

  return (
    <section className="recipe-section prefs-section">
      <h2>Delivery preferences</h2>

      {loading ? (
        <p className="muted">Loading…</p>
      ) : loadError ? (
        <p className="error">{loadError}</p>
      ) : (
        <form onSubmit={onSubmit} className="prefs-form">
          <label className="prefs-toggle">
            <input
              type="checkbox"
              checked={pushEnabled}
              onChange={(e) => setPushEnabled(e.target.checked)}
            />
            <span>
              <strong>Send push notifications</strong>
              <small className="muted">When off, push delivery is paused for every browser you've subscribed (manage subscriptions in <em>This browser</em> above).</small>
            </span>
          </label>

          <label className="prefs-toggle">
            <input
              type="checkbox"
              checked={emailEnabled}
              onChange={(e) => setEmailEnabled(e.target.checked)}
            />
            <span>
              <strong>Email</strong>
              <small className="muted">Not yet implemented on this instance — toggle has no effect today.</small>
            </span>
          </label>

          <fieldset className="prefs-quiet">
            <legend>Quiet hours</legend>
            <p className="muted prefs-help">
              During these hours, in-app notifications still appear but push delivery is suppressed.
            </p>
            <div className="prefs-quiet-row">
              <label className="field">
                <span>Start</span>
                <input
                  type="time"
                  value={quietStart}
                  onChange={(e) => setQuietStart(e.target.value)}
                />
              </label>
              <label className="field">
                <span>End</span>
                <input
                  type="time"
                  value={quietEnd}
                  onChange={(e) => setQuietEnd(e.target.value)}
                />
              </label>
              {(quietStart || quietEnd) && (
                <button
                  type="button"
                  className="link-button"
                  onClick={() => { setQuietStart(""); setQuietEnd(""); }}
                >
                  Clear
                </button>
              )}
            </div>
          </fieldset>

          <label className="field">
            <span>Timezone</span>
            <input
              type="text"
              value={timezone}
              onChange={(e) => setTimezone(e.target.value)}
              placeholder="America/Los_Angeles"
              spellCheck={false}
              autoCapitalize="off"
              autoCorrect="off"
            />
            <small className="muted">
              IANA name (e.g. <code>Europe/Berlin</code>). Used to interpret quiet hours.
            </small>
          </label>

          {saveError && <p className="error">{saveError}</p>}
          {saved    && <p className="muted">Saved.</p>}

          <div className="form-actions">
            <button type="submit" disabled={saving}>
              {saving ? "Saving…" : "Save preferences"}
            </button>
          </div>

          {prefs && prefs.timezone !== timezone && (
            <p className="muted prefs-help">
              Currently saved as <code>{prefs.timezone}</code>.
            </p>
          )}
        </form>
      )}
    </section>
  );
}

// --- helpers -------------------------------------------------------------

const RTF = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

function fmtRelative(iso: string, now: Date = new Date()): string {
  const target = new Date(iso);
  const deltaMs = target.getTime() - now.getTime();
  const abs = Math.abs(deltaMs);
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  const week = 7 * day;
  const year = 365 * day;
  if (abs < minute) return "just now";
  if (abs < hour)   return RTF.format(Math.round(deltaMs / minute), "minute");
  if (abs < day)    return RTF.format(Math.round(deltaMs / hour), "hour");
  if (abs < week)   return RTF.format(Math.round(deltaMs / day), "day");
  if (abs < year)   return RTF.format(Math.round(deltaMs / week), "week");
  return new Date(iso).toLocaleDateString(undefined, {
    year: "numeric", month: "short", day: "numeric",
  });
}

function fmtDateTime(iso: string): string {
  try {
    return new Date(iso).toLocaleString(undefined, {
      year: "numeric", month: "short", day: "numeric",
      hour: "2-digit", minute: "2-digit",
    });
  } catch {
    return iso;
  }
}
