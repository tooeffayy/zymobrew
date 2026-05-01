import { Link, NavLink, useNavigate } from "react-router-dom";

import { useAuth } from "../auth";
import { useNotifications } from "../notifications";

// Auth-aware site header. Renders nothing while auth state is loading
// so the nav doesn't flash anonymous links before we know the cookie's
// good — the user-facing flicker would read as "logged out for a beat".
export function Header() {
  const { state, logout } = useAuth();
  const { unread } = useNotifications();
  const navigate = useNavigate();

  if (state.status === "loading") {
    return (
      <header className="site-header">
        <Link to="/" className="brand">Zymo</Link>
      </header>
    );
  }

  const onLogout = async () => {
    await logout();
    navigate("/login", { replace: true });
  };

  return (
    <header className="site-header">
      <Link to="/" className="brand">Zymo</Link>
      <nav className="site-nav">
        <NavLink to="/" end>Recipes</NavLink>
        {state.status === "authed" && (
          <>
            <NavLink to="/batches">Batches</NavLink>
            <NavLink to="/notifications" className="nav-notifications" aria-label={unread > 0 ? `Notifications, ${unread} unread` : "Notifications"}>
              Notifications
              {unread > 0 && (
                // Cap at 99+ — three digits is the most that fits the pill
                // without growing the header height.
                <span className="nav-badge">{unread > 99 ? "99+" : unread}</span>
              )}
            </NavLink>
            <Link to="/recipes/new" className="header-cta">+ New recipe</Link>
            <NavLink to="/me">{state.user.display_name || state.user.username}</NavLink>
            <button type="button" className="link-button" onClick={onLogout}>Sign out</button>
          </>
        )}
        {state.status === "anon" && (
          <>
            <NavLink to="/login">Sign in</NavLink>
            <NavLink to="/register">Register</NavLink>
          </>
        )}
      </nav>
    </header>
  );
}
