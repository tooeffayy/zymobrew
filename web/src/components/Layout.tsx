import { ReactNode, useEffect, useState } from "react";
import { Link, useLocation } from "react-router-dom";

import { useAuth } from "../auth";
import { NotificationsBell } from "./NotificationsBell";
import { Sidebar } from "./Sidebar";
import { MenuIcon } from "./icons";

const COLLAPSE_KEY = "zymo.sidebar.collapsed";

// App shell: collapsible left sidebar + content column. On desktop the
// rail sits beside the content and collapses to icons (persisted). Below
// the CSS --shell-bp breakpoint the rail becomes an off-canvas drawer
// toggled by the top-bar hamburger; the drawer closes on route change,
// Escape, or backdrop click. Auth screens use AuthLayout, not this.
export function Layout({ children }: { children: ReactNode }) {
  const { state } = useAuth();
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(
    () => localStorage.getItem(COLLAPSE_KEY) === "1",
  );
  const [drawerOpen, setDrawerOpen] = useState(false);

  // Close the mobile drawer whenever the route changes.
  useEffect(() => {
    setDrawerOpen(false);
  }, [location.pathname]);

  // Escape closes the drawer (mirrors the modal/popover convention).
  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setDrawerOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drawerOpen]);

  const toggleCollapsed = () =>
    setCollapsed((v) => {
      const next = !v;
      localStorage.setItem(COLLAPSE_KEY, next ? "1" : "0");
      return next;
    });

  const authed = state.status === "authed";

  const cls =
    "app-shell" +
    (collapsed ? " app-shell--collapsed" : "") +
    (drawerOpen ? " app-shell--drawer" : "");

  return (
    <div className={cls}>
      <Sidebar
        collapsed={collapsed}
        onToggleCollapsed={toggleCollapsed}
        onNavigate={() => setDrawerOpen(false)}
      />
      {drawerOpen && (
        <div
          className="app-backdrop"
          onClick={() => setDrawerOpen(false)}
          aria-hidden="true"
        />
      )}
      <div className="app-content">
        <header className="app-topbar">
          <button
            type="button"
            className="app-menu-btn"
            aria-label="Open navigation"
            onClick={() => setDrawerOpen(true)}
          >
            <MenuIcon />
          </button>
          <Link to="/" className="brand app-topbar-brand">Zymo</Link>
          <div className="app-topbar-actions">
            {authed && (
              <Link to="/recipes/new" className="header-cta app-new-btn">
                + New recipe
              </Link>
            )}
            {authed && <NotificationsBell />}
          </div>
        </header>
        <main className="app-main">{children}</main>
      </div>
    </div>
  );
}
