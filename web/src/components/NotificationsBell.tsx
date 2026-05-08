import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";

import { Notification } from "../api";
import { useNotifications } from "../notifications";

// Bell button in the header. Click toggles a popover listing the
// user's unacknowledged (unread) notifications, with mark-read inline
// and a "View all" link to the inbox page. Closes on Escape, on
// click-outside, or on link navigation.
//
// Data is shared via NotificationsProvider, so the bell badge, the
// dashboard banner, and the inbox page all see the same `recent` page
// and stay in sync after mark-read.
export function NotificationsBell() {
  const { unread, unreadItems, markRead } = useNotifications();
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLDivElement | null>(null);

  // Close on Escape (consistent with modals) and on click outside the
  // dropdown wrapper. Both listeners are bound only while open so we
  // don't pay the cost on every render.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    const onClick = (e: MouseEvent) => {
      if (!wrapRef.current) return;
      if (!wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    // `mousedown` so the close fires before any click handler inside
    // the popover — otherwise tapping a link in the popover and the
    // toggle button in quick succession can get out of phase.
    window.addEventListener("mousedown", onClick);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("mousedown", onClick);
    };
  }, [open]);

  const label = unread > 0 ? `Notifications, ${unread} unread` : "Notifications";

  return (
    <div className="bell-wrap" ref={wrapRef}>
      <button
        type="button"
        className="bell-button"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="bell-icon" aria-hidden="true">🔔</span>
        <span className="bell-label">Notifications</span>
        {unread > 0 && (
          // Cap at 99+ — three digits is the most that fits the pill
          // without growing the header height.
          <span className="nav-badge">{unread > 99 ? "99+" : unread}</span>
        )}
      </button>
      {open && (
        <div className="bell-popover" role="menu">
          <header className="bell-popover-head">
            <strong>Unacknowledged</strong>
            <span className="muted">{unread} unread</span>
          </header>
          {unreadItems.length === 0 ? (
            <p className="muted bell-empty">You're all caught up.</p>
          ) : (
            <ul className="bell-list">
              {unreadItems.slice(0, 8).map((n) => (
                <BellItem
                  key={n.id}
                  n={n}
                  onMarkRead={() => void markRead(n.id)}
                  onNavigate={() => setOpen(false)}
                />
              ))}
            </ul>
          )}
          <footer className="bell-popover-foot">
            <Link to="/notifications" onClick={() => setOpen(false)}>
              View all →
            </Link>
          </footer>
        </div>
      )}
    </div>
  );
}

function BellItem({
  n,
  onMarkRead,
  onNavigate,
}: {
  n: Notification;
  onMarkRead: () => void;
  onNavigate: () => void;
}) {
  return (
    <li className="bell-item">
      <div className="bell-item-main">
        <div className="bell-item-head">
          <span className="bell-item-title">{n.title}</span>
          <span className="muted bell-item-when">{fmtRelative(n.created_at)}</span>
        </div>
        {n.body && <p className="bell-item-body">{n.body}</p>}
        {n.url_path && (
          <p className="bell-item-link">
            <Link to={n.url_path} onClick={onNavigate}>View →</Link>
          </p>
        )}
      </div>
      <button
        type="button"
        className="link-button bell-item-action"
        onClick={onMarkRead}
        aria-label={`Mark "${n.title}" read`}
      >
        ✓
      </button>
    </li>
  );
}

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
