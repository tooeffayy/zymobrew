# Zymo Web

React + Vite + TypeScript SPA. Compiled output is embedded into the Go
binary via `//go:embed all:dist` (see `web/embed.go`).

## Dev loop

Two terminals:

```sh
# Terminal 1: the Go API
docker compose up -d postgres
export DATABASE_URL=postgres://zymo:zymo@localhost:5433/zymo?sslmode=disable
go run ./cmd/zymo serve   # listens on :8080

# Terminal 2: the SPA dev server
cd web
npm install               # first time only
npm run dev               # listens on :5173
```

Open `http://localhost:5173`. Vite proxies `/api/*`, `/healthz`,
`/readyz`, and `/docs` to `:8080`, so the session cookie set by
`/api/auth/login` flows back on subsequent calls — same-origin from the
browser's perspective.

## Production build

```sh
cd web
npm install
npm run build      # writes ./dist/
cd ..
go build ./cmd/zymo
```

`go build` will then embed the freshly-built `web/dist/` into the binary.

If you skip the npm step and run `go build` directly, the binary still
compiles — but every non-API request returns a "frontend not built"
placeholder explaining the missing step. This keeps `go build` working
on a fresh `git clone` without forcing every Go contributor to install
Node.

## Layout

```
web/
  index.html
  package.json
  tsconfig.json
  vite.config.ts        — dev proxy + build output config
  embed.go              — //go:embed all:dist (Go side)
  src/
    main.tsx            — React entry, mounts <App>
    App.tsx             — BrowserRouter + route table
    api.ts              — fetch wrapper + ApiError + resource types
    auth.tsx            — AuthContext, useAuth, RequireAuth
    styles.css
    pages/
      Login.tsx
      Register.tsx
      Landing.tsx       — placeholder for the post-login experience
  dist/                 — Vite output, gitignored except .gitkeep
```

## Conventions

- **Auth**: cookie only. The session cookie (`zymo_session`,
  `HttpOnly`, `SameSite=Lax`) is set by `/api/auth/login` and sent
  automatically. No tokens stored in JS.
- **Errors**: `api.ts` throws `ApiError` with the server's `error`
  string; pages catch and render. No global error boundary yet.
- **Routing**: deep links work because the Go static handler falls
  back to `index.html` for any non-`/api` path that doesn't match a
  built asset.
- **Styles**: plain CSS in `src/styles.css`. No preprocessor or utility
  framework yet — keep deps lean until the design starts to need it.
