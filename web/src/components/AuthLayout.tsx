import { ReactNode } from "react";

// Centered single-card shell used by /login and /register. Kept separate
// from the main Layout so the bare auth screens stay chrome-free.
export function AuthLayout({ children }: { children: ReactNode }) {
  return <div className="auth-shell">{children}</div>;
}
