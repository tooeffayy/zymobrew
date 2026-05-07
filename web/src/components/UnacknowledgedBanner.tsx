import { Link } from "react-router-dom";

import { useNotifications } from "../notifications";

// Persistent banner showing the user's unacknowledged (unread)
// notifications. Renders nothing when there are none — the dashboard
// stays clean once the user is caught up.
//
// Shares state with the header bell via NotificationsProvider, so
// marking read here updates the bell badge instantly and vice versa.
//
// Defaults to showing the first MAX_VISIBLE items inline; anything
// beyond that hides behind a "+N more" link to the inbox page so the
// banner doesn't push the rest of the page off-screen during a flurry.
const MAX_VISIBLE = 5;

export function UnacknowledgedBanner() {
  const { unreadItems, markRead, markAllRead } = useNotifications();

  if (unreadItems.length === 0) return null;

  const visible = unreadItems.slice(0, MAX_VISIBLE);
  const overflow = unreadItems.length - visible.length;

  return (
    <section className="unack-banner" aria-label="Unacknowledged notifications">
      <header className="unack-head">
        <strong>
          {unreadItems.length} unacknowledged
          {unreadItems.length === 1 ? " notification" : " notifications"}
        </strong>
        <button
          type="button"
          className="link-button"
          onClick={() => void markAllRead()}
        >
          Mark all read
        </button>
      </header>
      <ul className="unack-list">
        {visible.map((n) => (
          <li key={n.id} className="unack-item">
            <div className="unack-item-main">
              <span className="unack-item-title">{n.title}</span>
              {n.body && <span className="unack-item-body"> — {n.body}</span>}
              {n.url_path && (
                <Link to={n.url_path} className="unack-item-link">View →</Link>
              )}
            </div>
            <button
              type="button"
              className="link-button"
              onClick={() => void markRead(n.id)}
              aria-label={`Mark "${n.title}" read`}
            >
              ✓
            </button>
          </li>
        ))}
      </ul>
      {overflow > 0 && (
        <p className="unack-overflow">
          <Link to="/notifications">+{overflow} more in inbox →</Link>
        </p>
      )}
    </section>
  );
}
