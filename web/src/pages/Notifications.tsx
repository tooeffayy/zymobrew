import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { ApiError, Notification, NotificationPage, api } from "../api";
import { useNotifications } from "../notifications";

// In-app notification inbox. The inbox here paginates independently of
// the global NotificationsProvider — that provider only holds page 1 to
// power the header bell and dashboard banner. Delivery preferences and
// per-browser push subscription live on the profile page (/me).
export function Notifications() {
  const { refresh: refreshGlobal, markRead: markReadGlobal, markAllRead: markAllReadGlobal } = useNotifications();

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
      // Patch in place — full refetch would jolt the scroll position
      // and lose any "load more" pages already in view.
      setItems((prev) =>
        prev.map((n) => (n.id === id && !n.read_at ? { ...n, read_at: new Date().toISOString() } : n)),
      );
      // Delegate the POST + global-cache update to the context so the
      // bell badge drops at the same instant.
      await markReadGlobal(id);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to mark read");
      void refreshGlobal();
    } finally {
      setBusy(false);
    }
  };

  const markAllRead = async () => {
    setBusy(true);
    try {
      const stamp = new Date().toISOString();
      setItems((prev) => prev.map((n) => (n.read_at ? n : { ...n, read_at: stamp })));
      await markAllReadGlobal();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "failed to mark all read");
      void refreshGlobal();
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

      <p className="muted">
        Manage delivery preferences and per-browser push on your <Link to="/me">profile page</Link>.
      </p>

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
