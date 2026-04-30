import { ReactNode } from "react";

import { Header } from "./Header";

// Page chrome for everything except the bare auth screens (which keep
// the centered single-card look). Children render inside <main>.
export function Layout({ children }: { children: ReactNode }) {
  return (
    <>
      <Header />
      <main className="site-main">{children}</main>
    </>
  );
}
