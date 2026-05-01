import { ReactNode } from "react";
import { NavLink } from "react-router-dom";

// Admin section chrome. Sub-nav across the top with one link per area.
// Wrap an admin page in <AdminLayout> and add a NavLink here when adding
// a new admin surface (audit log, instance settings, user search, …).
//
// The whole admin surface is gated on `users.is_admin` via RequireAdmin
// at the route level; this layout assumes the parent has already done
// the auth check.

export function AdminLayout({ children }: { children: ReactNode }) {
  return (
    <div className="admin-page">
      <header className="admin-header">
        <h1>Admin</h1>
        <nav className="admin-subnav">
          <NavLink to="/admin/backups">Backups</NavLink>
        </nav>
      </header>
      <div className="admin-content">{children}</div>
    </div>
  );
}
