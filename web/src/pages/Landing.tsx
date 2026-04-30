import { useNavigate } from "react-router-dom";

import { useAuth } from "../auth";

export function Landing() {
  const { state, logout } = useAuth();
  const navigate = useNavigate();

  // RequireAuth ensures we never render Landing in any other state, but
  // narrowing here keeps TS happy without a non-null assertion.
  if (state.status !== "authed") return null;
  const { user } = state;

  const onLogout = async () => {
    await logout();
    navigate("/login", { replace: true });
  };

  return (
    <div className="card">
      <h1>Welcome, {user.display_name || user.username}</h1>
      <dl>
        <dt>Username</dt>
        <dd>{user.username}</dd>
        <dt>Email</dt>
        <dd>{user.email}</dd>
        <dt>User ID</dt>
        <dd className="mono">{user.id}</dd>
      </dl>
      <p className="muted">
        Recipe browser, batch tracking, and calculators land in the next iteration.
      </p>
      <button onClick={onLogout}>Sign out</button>
    </div>
  );
}
