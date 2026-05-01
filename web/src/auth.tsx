import { createContext, useContext, useEffect, useState, ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";

import { api, ApiError, AuthResponse, PublicUser } from "./api";

// AuthState is one of three: still checking the cookie ("loading"),
// logged in with a known user, or logged out. Pages discriminate on
// `status` rather than checking `user` for null so the loading screen
// is distinguishable from the logged-out screen.
type AuthState =
  | { status: "loading" }
  | { status: "authed"; user: PublicUser }
  | { status: "anon" };

interface AuthCtx {
  state: AuthState;
  login: (identifier: string, password: string) => Promise<void>;
  register: (username: string, email: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
  // Update the cached user after a profile PATCH so the header reflects
  // a changed display name without a full /api/auth/me round-trip.
  updateUser: (user: PublicUser) => void;
  // Flip to anon without calling /api/auth/logout — used after account
  // deletion, where the server has already cleared the cookie and the
  // session row is gone.
  setAnon: () => void;
}

const AuthContext = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<AuthState>({ status: "loading" });

  // On mount, ask the server who we are. The cookie is sent automatically;
  // a 401 means anon, a 200 means authed. Any other error becomes anon —
  // a degraded server shouldn't leave the UI stuck on "loading" forever.
  useEffect(() => {
    api
      .get<PublicUser>("/api/auth/me")
      .then((user) => setState({ status: "authed", user }))
      .catch(() => setState({ status: "anon" }));
  }, []);

  const login = async (identifier: string, password: string) => {
    const res = await api.post<AuthResponse>("/api/auth/login", { identifier, password });
    setState({ status: "authed", user: res.user });
  };

  const register = async (username: string, email: string, password: string) => {
    const res = await api.post<AuthResponse>("/api/auth/register", { username, email, password });
    setState({ status: "authed", user: res.user });
  };

  const logout = async () => {
    try {
      await api.post("/api/auth/logout");
    } catch (e) {
      // A 401 here means we're already logged out — fine.
      if (!(e instanceof ApiError) || e.status !== 401) throw e;
    }
    setState({ status: "anon" });
  };

  const updateUser = (user: PublicUser) => setState({ status: "authed", user });
  const setAnon = () => setState({ status: "anon" });

  return (
    <AuthContext.Provider value={{ state, login, register, logout, updateUser, setAnon }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthCtx {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside <AuthProvider>");
  return ctx;
}

// RequireAuth wraps protected routes. While auth state is loading we
// render nothing rather than flash the login page; once known, we
// either render children or redirect with the original location stashed
// in state so login can return the user there afterward.
export function RequireAuth({ children }: { children: ReactNode }) {
  const { state } = useAuth();
  const location = useLocation();
  if (state.status === "loading") return null;
  if (state.status === "anon") return <Navigate to="/login" state={{ from: location }} replace />;
  return <>{children}</>;
}
