import { Link, NavLink, useNavigate } from "react-router-dom";

import { useAuth } from "../auth";
import { NotificationsBell } from "./NotificationsBell";

// Auth-aware site header. Renders nothing while auth state is loading
// so the nav doesn't flash anonymous links before we know the cookie's
// good — the user-facing flicker would read as "logged out for a beat".
export function Header() {
  const { state, logout } = useAuth();
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
        <NavLink to="/calculators">Calculators</NavLink>
        {state.status === "authed" && (
          <>
            <NavLink to="/batches">Batches</NavLink>
            <NavLink to="/inventory">Inventory</NavLink>
            <NotificationsBell />
            <Link to="/recipes/new" className="header-cta">+ New recipe</Link>
            {state.user.is_admin && <NavLink to="/admin">Admin</NavLink>}
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
