import { FormEvent, useState } from "react";
import { Link, useNavigate } from "react-router-dom";

import { ApiError } from "../api";
import { useAuth } from "../auth";

// Register is a single screen for all three INSTANCE_MODEs:
// - open:        always works
// - single_user: works once (bootstraps admin), 403 thereafter
// - closed:      always 403 with "registration disabled"
// We don't gate the UI by mode — the server's response is authoritative
// and shows the user the right message either way.
export function Register() {
  const { register } = useAuth();
  const navigate = useNavigate();

  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const onSubmit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await register(username, email, password);
      navigate("/", { replace: true });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "registration failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="card">
      <h1>Create account</h1>
      <form onSubmit={onSubmit}>
        <label>
          Username
          <input
            type="text"
            autoComplete="username"
            required
            pattern="[a-zA-Z0-9_-]{3,32}"
            title="3–32 chars: letters, digits, _ or -"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </label>
        <label>
          Email
          <input
            type="email"
            autoComplete="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>
        <label>
          Password
          <input
            type="password"
            autoComplete="new-password"
            required
            minLength={8}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        {error && <p className="error">{error}</p>}
        <button type="submit" disabled={submitting}>
          {submitting ? "Creating…" : "Create account"}
        </button>
      </form>
      <p>
        Have an account? <Link to="/login">Sign in</Link>
      </p>
    </div>
  );
}
