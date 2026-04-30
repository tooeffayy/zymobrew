import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite serves the SPA on :5173 in dev. Anything under /api proxies to the Go
// server on :8080 — same-origin from the browser's perspective, so the
// session cookie set by /api/auth/login is sent on subsequent /api/* calls
// without any cross-origin / SameSite adjustments. /healthz, /readyz, and
// /docs are forwarded too because the SPA links to /docs.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api":     "http://localhost:8080",
      "/healthz": "http://localhost:8080",
      "/readyz":  "http://localhost:8080",
      "/docs":    "http://localhost:8080",
    },
  },
  build: {
    // Production assets are picked up by //go:embed in internal/server.
    outDir: "dist",
    emptyOutDir: true,
  },
});
