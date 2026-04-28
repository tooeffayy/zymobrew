# Zymo — Design Notes

A self-hostable fermentation tracking app. Name from *zymurgy*, the science of fermentation.

## Project Overview

- **Deployment model**: self-hosted (homelab / VPS / Docker); SaaS deferred
- **Audience**: brewers — solo loggers, family/friends instances, or open public instances
- **Fermentation types** (eventual): mead, beer, cider, wine, kombucha
- **MVP scope**: mead only; expand to other types in later phases
- **Platforms**: web (built into the binary) + mobile (points at instance URL, Mastodon-style)
- **License**: AGPL v3 + CLA (preserves option to dual-license commercially)

## Tech Stack

**Backend (Go)** — single binary, embeds compiled web frontend.
- `chi` — HTTP router
- `pgx` + `sqlc` — Postgres driver with type-safe generated query code
- `goose` — schema migrations, run automatically on startup
- `River` — Postgres-native background jobs. **No Redis dependency.**
- Local accounts default; OIDC/OAuth optional via env config

**Frontend**: Expo + React Native Web — one codebase, iOS/Android/web. Mobile prompts for instance URL on first launch (Mastodon-style).

**Database**: Postgres 14+. Uses CITEXT, ENUMs, JSONB + GIN, partial indexes, recursive CTEs, `tsvector`.

**Storage**: Local disk default; S3-compatible optional. Abstracted behind an interface.

## Project Layout

```
cmd/zymo/             entry point (serve | migrate | selftest | version)
internal/auth         argon2id password hashing + session token primitives
internal/config       env-based config loader
internal/db           pgx pool + database/sql open helpers
internal/jobs         River client + workers (background jobs, periodic schedules)
internal/migrate      goose runner + River migrator (uses embedded migrations)
internal/queries      sqlc generated type-safe code (Go only)
internal/queries/sql  sqlc query source files (*.sql)
internal/ratelimit    in-memory token-bucket limiter (per-IP, per-identifier)
internal/server       chi HTTP router — /healthz, /readyz, /api/auth/*, /api/users/*, /api/recipes/*, /api/batches/*
internal/selftest     runtime smoke tests for `zymo selftest`
internal/testutil     shared DB test setup
migrations/           embedded SQL migrations + embed.go
```

Migrations are baked into the binary via `//go:embed`.

## Development

```
docker compose up -d postgres
export DATABASE_URL=postgres://zymo:zymo@localhost:5433/zymo?sslmode=disable
go run ./cmd/zymo serve
```

| Command         | Purpose |
|-----------------|---------|
| `zymo serve`    | Runs HTTP server. Auto-migrates unless `AUTO_MIGRATE=false`. |
| `zymo migrate`  | Apply pending migrations and exit. |
| `zymo selftest` | Smoke-check a live instance (connect → ping → schema → CRUD round-trip). |
| `zymo version`  | Print build version. |

**Regenerating queries**: edit `internal/queries/sql/*.sql`, then run `$(go env GOPATH)/bin/sqlc generate`. Generated files are committed.

**Environment variables**

| Var             | Default       | Notes |
|-----------------|---------------|-------|
| `DATABASE_URL`    | *(required)*            | Postgres connection URL |
| `LISTEN_ADDR`     | `:8080`                 | HTTP listen address |
| `INSTANCE_MODE`   | `single_user`           | `single_user` \| `closed` \| `open` |
| `AUTO_MIGRATE`    | `true`                  | Apply pending migrations on `serve` startup |
| `COOKIE_SECURE`   | `false`                 | Set true in production behind TLS |
| `VAPID_PUBLIC_KEY`  | *(optional)*          | VAPID public key for web-push (generate with `zymo vapid-keys`) |
| `VAPID_PRIVATE_KEY` | *(optional)*          | VAPID private key for web-push |
| `VAPID_SUBJECT`     | `mailto:admin@localhost` | VAPID contact (mailto: or https:) |

## Tests + Smoke Checks

```
docker compose up -d postgres
export TEST_DATABASE_URL=postgres://zymo:zymo@localhost:5433/zymo?sslmode=disable
go test ./...
```

`testutil.Pool(t, ctx)` migrates the schema once per process; tests isolate via `TRUNCATE` or `pool.Begin` + `Rollback`. DB-backed tests skip without `TEST_DATABASE_URL`.

`zymo selftest` is the runtime equivalent — use after every deploy.

---

## Phased Roadmap

1. ~~Auth (local), profile, mead batch CRUD with readings + chart~~ ✓
2. ~~Recipes, forking, instance feed, comments~~ ✓
3. ~~Calculators + reminders / web-push notifications~~ ✓ (calculators deferred; reminders + web-push done)
4. Backup + export (first-class)
5. Cider + wine (reuse ~90% of mead flow)
6. Beer (new flows: mash, boil, IBU)
7. Kombucha (continuous fermentation model — F1/F2)

## Instance Modes

- **Single-user** — first registration bootstraps the admin; closed after that.
- **Closed** — registration off entirely; users created out-of-band (CLI bootstrap to come).
- **Open** — anyone can register.

## Auth

- **Password**: argon2id (m=64MB, t=1, p=4) in PHC string format. Nullable so OIDC users coexist later.
- **Sessions**: opaque 32-byte random token; SHA-256 stored in `sessions.token_hash`. Default 30-day lifetime.
- **Transport**: `Cookie: zymo_session` (HttpOnly, SameSite=Lax) or `Authorization: Bearer` — same session row, revocation is one DELETE.
- **Password change** rotates all sessions: deletes all existing sessions atomically before issuing a new one, so a compromised token can't survive the change.
- **Login timing**: always runs argon2 (against a dummy hash if user not found) to prevent username enumeration by timing. See `auth.DummyHash`.
- **Cookie security**: `COOKIE_SECURE=true` for production. CSRF currently SameSite=Lax; CSRF tokens deferred until cross-origin frontends exist.

### Known auth gaps (deferred)

- **Single-user TOCTOU** — `CountUsers` + `INSERT` are two queries; concurrent bootstraps could both succeed. Fix: SERIALIZABLE tx.
- **Rate-limit state is in-process** — multi-replica deployments leak headroom. Move to shared store when multi-replica ships.
- **No trusted-proxy config** — `middleware.RealIP` blindly trusts `X-Forwarded-For`. Add allowlist when warranted.
- **No `last_seen_at` touch on activity** — `TouchSession` exists but isn't called. Decide strategy (every request vs. rate-limited) when activity feed needs it.
- **No client IP on sessions** — `sessions.ip` is `INET NULL`; populate once trusted-proxy policy is settled.

## Login Rate Limiting

Two layers, in-memory token buckets (`internal/ratelimit`). Per-process — acceptable for single-replica baseline.

| Layer | Where | Burst | Refill | Keyed by |
|---|---|---|---|---|
| IP gate | middleware on `/api/auth/{register,login}` | 10 | 1 / 2s | `r.RemoteAddr` (post chi `RealIP`) |
| Per-identifier gate | inside `handleLogin` after body decode | 5 | 1 / 12s | `strings.ToLower(req.Identifier)` |

Either trip returns 429 with `Retry-After: 60`. Eviction is lazy — no background goroutine.

## Profile API

- `GET /api/users/{username}` — public profile (id, username, display_name, bio, avatar_url, created_at). No email.
- `PATCH /api/users/me` — update display_name / bio / avatar_url. Caps: display_name 64 chars, bio 2 KiB, avatar_url 512 bytes. COALESCE pattern — omitted fields unchanged.
- `POST /api/users/me/password` — change password. Verifies current, rejects same-as-current, rotates all sessions.

## Recipes API

`GET /api/recipes` — public feed, `visibility = 'public'`, newest first. Query params: `limit` (default 20, max 100), `offset`.
`POST /api/recipes` — requires auth. MVP guard: rejects `brew_type != mead`. Returns full recipe view with revision 1.
`GET /api/recipes/mine` — requires auth. Returns all recipes for the authenticated user (all visibilities), newest first.
`GET /api/recipes/{id}` — returns recipe with live ingredients. Visibility rules: `public` or `unlisted` = anyone; `private` = owner only (404 for others — existence not leaked).
`PATCH /api/recipes/{id}` — requires auth, owner only. COALESCE pattern for meta fields. **Always creates a new revision.** Replaces ingredients wholesale (DELETE + re-INSERT). Returns updated recipe view.
`DELETE /api/recipes/{id}` — requires auth, owner only.
`POST /api/recipes/{id}/fork` — requires auth. Creates a private copy of the recipe pinned to the source's current revision. Optional body: `name` (override), `message` (revision 1 message). Private recipes return 404 to non-owners (existence not leaked). Self-fork and fork-of-fork both allowed. Increments `fork_count` on source atomically in the same transaction.
`GET /api/recipes/{id}/revisions` — summary list (no ingredients). Publicly gated same as GET.
`GET /api/recipes/{id}/revisions/{rev}` — full revision detail; `ingredients` is the JSONB snapshot from that point in time.

**Validation caps**: name 200 chars, style 100 chars, description 10 KiB, max 50 ingredients per recipe.

**Revision semantics**: every PATCH creates an immutable revision row. `revision_count` auto-increments in SQL via `SetRecipeRevision`. Revision numbers are per-recipe integers starting at 1. `current_revision_id` on the recipe row is the O(1) HEAD pointer.

**Visibility model**: `public` (in feed + direct), `unlisted` (direct only, not in feed), `private` (owner only). All unauthorized access returns 404, not 403.

**Transaction pattern**: create/update use `s.pool.Begin` + `s.queries.WithTx(tx)` — first use of explicit transactions in this codebase. Pattern: `defer tx.Rollback(ctx)` as safety net, explicit `tx.Commit(ctx)` at end.

## Reminders + Notifications API

### Batch Reminders

All under `/api/batches/{id}/reminders`, requires auth + batch ownership (404 for others).

`POST /api/batches/{id}/reminders` — create a manual reminder. Body: `title` (required), `description`, `fire_at` (RFC3339, required), `suggested_event_kind`. Returns reminder.
`GET /api/batches/{id}/reminders` — list reminders for a batch, ordered by `fire_at ASC`.
`PATCH /api/batches/{id}/reminders/{reminderId}` — update title/description/fire_at/status/suggested_event_kind. COALESCE pattern. Allowed status values: `scheduled`, `snoozed`, `completed`, `dismissed`.
`DELETE /api/batches/{id}/reminders/{reminderId}` — cancels (sets `status=cancelled`). Returns 204. 404 if already fired or completed.

**Reminder status lifecycle**: `scheduled` → (fired by dispatcher) → `fired` → user marks `completed` or `dismissed`. Can be `snoozed` (reschedule `fire_at`). Cancelled from any non-terminal state.

**Dispatcher**: River job `reminder_dispatcher` runs every minute. Atomically claims all `WHERE status='scheduled' AND fire_at <= now()` in batches of 100 using `FOR UPDATE SKIP LOCKED`. Creates one `notifications` row per reminder.

### Notifications Inbox

`GET /api/notifications` — requires auth. Query params: `limit` (default 20, max 100), `offset`.
`POST /api/notifications/{id}/read` — mark one notification read (204). 404 if not found.
`POST /api/notifications/read-all` — mark all unread as read (204).

### Notification Prefs

`GET /api/notifications/prefs` — returns prefs (or defaults if not set: push_enabled=true, email_enabled=false, timezone=UTC).
`PATCH /api/notifications/prefs` — upsert. Fields: `push_enabled`, `email_enabled`, `quiet_hours_start` ("HH:MM"), `quiet_hours_end` ("HH:MM"), `timezone` (IANA tz string). Quiet hours: suppresses push delivery (not in-app creation) — deferred until web-push is added.

### Recipe Reminder Templates

`GET /api/recipes/{id}/reminder-templates` — lists templates; visibility rules same as recipe GET.
`POST /api/recipes/{id}/reminder-templates` — requires auth, owner only. Fields: `title` (required), `description`, `anchor` (default `pitch`; `absolute` rejected), `offset_minutes`, `suggested_event_kind`, `sort_order`.
`PATCH /api/recipes/{id}/reminder-templates/{templateId}` — requires auth, owner only. COALESCE pattern.
`DELETE /api/recipes/{id}/reminder-templates/{templateId}` — requires auth, owner only. 204.

**`absolute` anchor** — rejected on recipe templates (no wall-clock date to resolve against). Valid only on direct batch reminders where the user supplies `fire_at` directly.

**Materialization** — triggered automatically (best-effort, non-blocking):
- `POST /api/batches` with `recipe_id` + `started_at` → materializes `batch_start` templates
- `PATCH /api/batches/{id}` with `started_at` → materializes `batch_start` templates
- `POST /api/batches/{id}/events` with kind `pitch`/`rack`/`bottle` → materializes corresponding anchor templates

`MaterializeReminderTemplates` uses `NOT EXISTS` to prevent double-materialization. Template anchor `custom_event` is stored but not auto-materialized (deferred).

**Batch–recipe linkage** — `POST /api/batches` now accepts optional `recipe_id`; pins to `current_revision_id` at creation time. Private recipes reject linking by non-owners. Recipe association cannot be changed after creation.

### Web-Push (VAPID)

`GET /api/push/public-key` — returns `{"public_key": "..."}` for browser subscription. 404 if not configured.
`POST /api/push/subscribe` — requires auth. Body: `{"endpoint": "...", "keys": {"p256dh": "...", "auth": "..."}}`. Upserts `push_devices` row. Returns `{"id": "..."}`.
`POST /api/push/unsubscribe` — requires auth. Body: `{"endpoint": "..."}`. 204.

**VAPID keys**: generate with `zymo vapid-keys` (prints env var lines). Set `VAPID_PUBLIC_KEY`, `VAPID_PRIVATE_KEY`, and optionally `VAPID_SUBJECT` (default `mailto:admin@localhost`). If not set, push is silently skipped but in-app notifications still work.

**Quiet hours**: dispatcher checks user's `notification_prefs.quiet_hours_*` (in user's timezone) before sending push. Handles midnight-wrapping windows (e.g. 22:00–06:00). In-app notifications are always created regardless of quiet hours.

**Push payload**: JSON `{"title": "...", "body": "...", "url_path": "..."}`. Browser service worker receives this and shows a native notification.

### Known gaps (deferred)

- **Calculators** — ABV, OG→FG, honey weight, pitch rate. Self-contained API endpoints, no DB needed. Deferred from Phase 3.
- **`custom_event` anchor** materialization — needs a way to specify which event title/kind to anchor to.
- **Re-materialization on re-anchor** — if a batch's pitch event changes (edited `occurred_at`), existing reminders are not updated.
- **Push subscription cleanup** — expired/invalid subscriptions (410 Gone from push service) should be deleted automatically.

### Known recipe API gaps (deferred)

- ~~**No forking**~~ — `POST /api/recipes/{id}/fork` implemented. Forks default to `private`; owner can PATCH visibility.
- **No pagination cursor** — uses limit/offset; add cursor when feed grows large.
- **PATCH can't clear to NULL** — same as batches.
- **No optimistic concurrency** — last-write-wins on PATCH.

## Batches API

All `/api/batches/*` requires auth. 404 (not 403) for other users' rows — existence not leaked. MVP guard: rejects `brew_type != mead`.

**PATCH semantics** — `COALESCE(narg, col)` pattern. Omitted fields unchanged. Cannot clear to NULL yet.

**NUMERIC handling** — gravity/temp/pH are `pgtype.Numeric`. Handlers use `numericPtr`/`floatToNumeric` helpers. sqlc doesn't accept Go pointer types in nullable overrides.

### Known batch API gaps (deferred)

- **Unbounded list responses** — readings/events return all rows. Add cursor pagination when device adapters (Tilt/RAPT) land.
- **`source` is free text** — constrain to known set when device adapters ship.
- **Race: ownership check + insert** — two queries; fold into `INSERT ... WHERE EXISTS` when motivated.
- **PATCH can't clear to NULL** — add per-field clear handling when a real workflow needs it.
- **No optimistic concurrency** — last-write-wins on PATCH. Add If-Match when multi-device matters.

## Background Jobs

River runs in-process. Queue state in `river_*` tables. `migrate.Up` runs goose then River's migrator.

| Job | Schedule | Purpose |
|---|---|---|
| `expired_sessions_gc` | every hour, `RunOnStart: true` | `DELETE FROM sessions WHERE expires_at < now()` |
| `reminder_dispatcher` | every minute, `RunOnStart: false` | Atomically claims due reminders (`FOR UPDATE SKIP LOCKED`), creates in-app `notifications` rows |

**Adding a job**: new file `internal/jobs/<name>.go` with `<Name>Args` + `Kind()` + worker embedding `river.WorkerDefaults`. Register in `New()`. Test by constructing a synthetic `*river.Job[Args]` and calling `Work()` directly.

## Core Concepts

- A **batch is a timeline**. Recipe = the plan; batch = what actually happened.
- **Readings** (gravity, temp, pH) — quantitative, power the chart.
- **Events** (rack, dry hop, bottle) — qualitative, power the journal.
- **Tasting notes** — first-class; brewers add multiple as the brew ages.
- **Recipes are forkable** with full revision history; batches pin to exact recipe revision.

---

## Design Decisions

### Core schema
- **Recipe vs batch ingredients are separate tables** — recipe is the plan, batch records what was actually used.
- **Hybrid normalized + JSONB ingredients** — common columns for querying; `details` JSONB for kind-specific fields. New kinds = enum value, no schema migration.
- **Readings vs events split** — readings power the chart; events are the journal. Combining hurts both.
- **`source` on readings** — future-proofs Tilt/RAPT hardware integration with no schema change.
- **`brew_type` denormalized on batches** — filters without joining to recipe; freeform batches have no recipe.
- **Split comments/likes per type** — FK integrity and trivial queries beat polymorphic associations.

### Recipe revisions
- **JSONB ingredient snapshots** — revisions are immutable history, not relational query targets. One row per revision.
- **`current_revision_id`** — O(1) HEAD lookup, no `MAX(revision_number)` scan.
- **Batches pin to `recipe_revision_id`** — old batch logs reflect what was actually brewed even after recipe changes.
- **Per-recipe revision numbers** — readable URLs (`/recipes/orange-mead/v3`).
- **Revisions created in service layer**, not by trigger — control when a snapshot warrants saving.

### Forking model
- **Fork = new recipe with `parent_id` + `parent_revision_id`** — pins exact version, copies ingredients to fresh rows.
- **Fork in single transaction**, increments `fork_count` on source.
- **Cannot fork private**; can fork public or unlisted.
- **Allow forking own recipes** and re-forking forks — full lineage chain.
- **No "merge upstream"** — brewing recipes aren't bug-fixed; forkers don't want author changes auto-applied.
- **Don't force "(fork)" in name** — attribution via "Forked from @user/recipe" is cleaner.

### Reminders
- **Templates on recipes** materialize into concrete reminders when a batch starts; re-resolve on stage transitions.
- **DB poll dispatcher** (every minute) — chosen over queue-at-creation because reminders are frequently edited/cancelled.
- **Atomic claim** before dispatch prevents double-send.
- **In-app notifications always created**; push/email gated by prefs.
- **Quiet hours respect user timezone** — store TZ on prefs, never compute against UTC.
- **Smart reminders**: no reading in N days → gravity check nudge; stable gravity across 3+ readings → racking suggestion; stage → `aging` → auto-schedule milestones.

---

> Future-phase schemas (Phase 2+ DB schema, reminders, community tables), useful queries, deferred feature notes, backup/export, account deletion, and hardware integration are in [docs/design-reference.md](docs/design-reference.md).

## Open Decisions

- **CLI framework** — stdlib `os.Args` switch for now. Switch to `spf13/cobra` when first subcommand grows a flag or command count passes ~6.

## Non-Goals (For Now)

- **Federation** — no ActivityPub. Instances are islands; data moves via export/import.
- **Hosted SaaS** — AGPL + CLA preserves the option; not the current focus.
- **Native iOS/Android push** — requires Apple/Google relay. Web Push (VAPID) covers it.
