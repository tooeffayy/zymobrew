import { createContext, ReactNode, useCallback, useContext, useEffect, useRef, useState } from "react";

import { ApiError, Notification, NotificationPage, api } from "./api";
import { useAuth } from "./auth";

// Shared notification state so the header badge, the inbox page, and
// any other surface stay in sync without each one polling separately.
//
// Strategy:
//  - When auth flips to "authed", fetch the first page once.
//  - Re-fetch every 90s in the background — fresh enough to surface a
//    just-fired reminder without hammering the server. Brewing
//    notifications are low-volume; a tighter interval would burn
//    requests for almost nothing.
//  - Pages call refresh() after mark-read / mark-all so the badge
//    drops immediately, no waiting on the next poll.
//
// Only the *first page* of notifications is held here — that's all the
// header needs to compute "any unread?". The inbox page paginates
// independently via its own state.

const POLL_MS = 90_000;

interface NotificationsCtx {
  unread: number;
  recent: Notification[];
  refresh: () => Promise<void>;
}

const Ctx = createContext<NotificationsCtx | null>(null);

export function NotificationsProvider({ children }: { children: ReactNode }) {
  const { state } = useAuth();
  const [recent, setRecent] = useState<Notification[]>([]);

  // Avoid a stale fetch from a previous user landing in state after
  // login → logout → login. Each fetch tags itself with the current
  // user's id; if the id has changed by the time the response lands
  // we drop it on the floor.
  const userIDRef = useRef<string | null>(null);
  userIDRef.current = state.status === "authed" ? state.user.id : null;

  const refresh = useCallback(async () => {
    const expectedUser = userIDRef.current;
    if (!expectedUser) return;
    try {
      const page = await api.get<NotificationPage>("/api/notifications?limit=20");
      if (userIDRef.current !== expectedUser) return;
      setRecent(page.notifications);
    } catch (e) {
      // 401 means the cookie expired — leave state alone, the auth
      // provider's next /me call will flip us to "anon" and the
      // effect below will clear `recent`.
      if (e instanceof ApiError && e.status === 401) return;
      // Any other transient failure: keep the last good page.
    }
  }, []);

  useEffect(() => {
    if (state.status !== "authed") {
      setRecent([]);
      return;
    }
    void refresh();
    const id = window.setInterval(() => { void refresh(); }, POLL_MS);
    return () => window.clearInterval(id);
  }, [state.status, refresh]);

  const unread = recent.reduce((acc, n) => (n.read_at ? acc : acc + 1), 0);

  return (
    <Ctx.Provider value={{ unread, recent, refresh }}>
      {children}
    </Ctx.Provider>
  );
}

export function useNotifications(): NotificationsCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useNotifications must be used inside <NotificationsProvider>");
  return ctx;
}
