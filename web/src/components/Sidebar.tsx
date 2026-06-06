import { Link, NavLink, useNavigate } from "react-router-dom";

import { useAuth } from "../auth";
import {
  AdminIcon,
  BatchesIcon,
  CalculatorsIcon,
  CollapseIcon,
  InventoryIcon,
  RecipesIcon,
  SignOutIcon,
  UserIcon,
} from "./icons";

// Sidebar rail for the app shell. Brand at the top, primary nav in the
// middle, user identity + sign-out pinned to the foot. Labels collapse
// away (icons remain) when `collapsed`; on mobile the whole rail is an
// off-canvas drawer (positioning handled in CSS) and `onNavigate` closes
// it after a link is followed.
//
// className for nav items is a function so the active route picks up the
// `active` style — NavLink passes { isActive }.
function navClass({ isActive }: { isActive: boolean }) {
  return isActive ? "app-nav-item active" : "app-nav-item";
}

export function Sidebar({
  collapsed,
  onToggleCollapsed,
  onNavigate,
}: {
  collapsed: boolean;
  onToggleCollapsed: () => void;
  onNavigate: () => void;
}) {
  const { state, logout } = useAuth();
  const navigate = useNavigate();
  const user = state.status === "authed" ? state.user : null;

  const onLogout = async () => {
    onNavigate();
    await logout();
    navigate("/login", { replace: true });
  };

  return (
    <aside className="app-sidebar">
      <div className="app-sidebar-head">
        <Link to="/" className="brand app-brand" onClick={onNavigate}>
          <span className="app-brand-word">Zymo</span>
        </Link>
      </div>

      <nav className="app-nav">
        <NavLink to="/" end className={navClass} onClick={onNavigate}>
          <span className="app-nav-icon"><RecipesIcon /></span>
          <span className="app-nav-label">Recipes</span>
        </NavLink>
        <NavLink to="/batches" className={navClass} onClick={onNavigate}>
          <span className="app-nav-icon"><BatchesIcon /></span>
          <span className="app-nav-label">Batches</span>
        </NavLink>
        <NavLink to="/inventory" className={navClass} onClick={onNavigate}>
          <span className="app-nav-icon"><InventoryIcon /></span>
          <span className="app-nav-label">Inventory</span>
        </NavLink>
        <NavLink to="/calculators" className={navClass} onClick={onNavigate}>
          <span className="app-nav-icon"><CalculatorsIcon /></span>
          <span className="app-nav-label">Calculators</span>
        </NavLink>
        {user?.is_admin && (
          <NavLink to="/admin" className={navClass} onClick={onNavigate}>
            <span className="app-nav-icon"><AdminIcon /></span>
            <span className="app-nav-label">Admin</span>
          </NavLink>
        )}
      </nav>

      <div className="app-foot">
        <NavLink to="/me" className={navClass} onClick={onNavigate}>
          <span className="app-nav-icon"><UserIcon /></span>
          <span className="app-nav-label">
            {user?.display_name || user?.username || "Account"}
          </span>
        </NavLink>
        <button type="button" className="app-nav-item" onClick={onLogout}>
          <span className="app-nav-icon"><SignOutIcon /></span>
          <span className="app-nav-label">Sign out</span>
        </button>
        <button
          type="button"
          className="app-nav-item app-collapse-btn"
          onClick={onToggleCollapsed}
          aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
          aria-pressed={collapsed}
        >
          <span className="app-nav-icon"><CollapseIcon /></span>
          <span className="app-nav-label">Collapse</span>
        </button>
      </div>
    </aside>
  );
}
