import { useAuth } from "../auth";

// Authenticated user's profile snapshot. Sign-out lives in the header
// now, so this page is just a read-only view until profile editing
// lands.
export function Me() {
  const { state } = useAuth();

  // RequireAuth guards this route, but narrow here to keep TS happy
  // without a non-null assertion.
  if (state.status !== "authed") return null;
  const { user } = state;

  return (
    <div className="card">
      <h1>{user.display_name || user.username}</h1>
      <dl>
        <dt>Username</dt>
        <dd>{user.username}</dd>
        <dt>Email</dt>
        <dd>{user.email}</dd>
        <dt>User ID</dt>
        <dd className="mono">{user.id}</dd>
      </dl>
    </div>
  );
}
