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
| `DATABASE_URL`  | *(required)*  | Postgres connection URL |
| `LISTEN_ADDR`   | `:8080`       | HTTP listen address |
| `INSTANCE_MODE` | `single_user` | `single_user` \| `closed` \| `open` |
| `AUTO_MIGRATE`  | `true`        | Apply pending migrations on `serve` startup |
| `COOKIE_SECURE` | `false`       | Set true in production behind TLS |

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
2. **Recipes, forking, instance feed, comments** ← in progress (Recipe CRUD + revisions + forking done; comments, likes/feed remaining)
3. Calculators + reminders / web-push notifications
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

**Adding a job**: new file `internal/jobs/<name>.go` with `<Name>Args` + `Kind()` + worker embedding `river.WorkerDefaults`. Register in `New()`. Test by constructing a synthetic `*river.Job[Args]` and calling `Work()` directly.

## Core Concepts

- A **batch is a timeline**. Recipe = the plan; batch = what actually happened.
- **Readings** (gravity, temp, pH) — quantitative, power the chart.
- **Events** (rack, dry hop, bottle) — qualitative, power the journal.
- **Tasting notes** — first-class; brewers add multiple as the brew ages.
- **Recipes are forkable** with full revision history; batches pin to exact recipe revision.

---

## Database Schema — Phase 2+ (not yet migrated)

The already-shipped schema is in `migrations/`. Below is the planned schema for upcoming phases.

### Enums (additions needed)

```sql
CREATE TYPE reminder_status AS ENUM
  ('scheduled','fired','completed','snoozed','dismissed','cancelled');
CREATE TYPE reminder_anchor AS ENUM
  ('absolute','batch_start','pitch','rack','bottle','custom_event');
```

Existing enums already in migrations: `brew_type`, `visibility`, `batch_stage`, `ingredient_kind`, `event_kind`.

### Recipes (with revisions + forking)

```sql
CREATE TABLE recipes (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  author_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_id           UUID REFERENCES recipes(id) ON DELETE SET NULL,
  parent_revision_id  UUID REFERENCES recipe_revisions(id),
  current_revision_id UUID REFERENCES recipe_revisions(id),
  revision_count      INT NOT NULL DEFAULT 0,
  fork_count          INT NOT NULL DEFAULT 0,
  brew_type           brew_type NOT NULL,
  style               TEXT,
  name                TEXT NOT NULL,
  description         TEXT,
  target_og           NUMERIC(5,3),
  target_fg           NUMERIC(5,3),
  target_abv          NUMERIC(4,2),
  batch_size_l        NUMERIC(6,2),
  visibility          visibility NOT NULL DEFAULT 'public',
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON recipes(author_id);
CREATE INDEX ON recipes(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX ON recipes(brew_type, visibility);

CREATE TABLE recipe_ingredients (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id  UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  kind       ingredient_kind NOT NULL,
  name       TEXT NOT NULL,
  amount     NUMERIC(10,3),
  unit       TEXT,
  sort_order INT NOT NULL DEFAULT 0,
  details    JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX ON recipe_ingredients(recipe_id);

CREATE TABLE recipe_revisions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id       UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  revision_number INT NOT NULL,
  author_id       UUID NOT NULL REFERENCES users(id),
  message         TEXT,
  name            TEXT NOT NULL,
  description     TEXT,
  style           TEXT,
  target_og       NUMERIC(5,3),
  target_fg       NUMERIC(5,3),
  target_abv      NUMERIC(4,2),
  batch_size_l    NUMERIC(6,2),
  ingredients     JSONB NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (recipe_id, revision_number)
);
CREATE INDEX ON recipe_revisions(recipe_id, revision_number DESC);
CREATE INDEX ON recipe_revisions(author_id);
```

Note: `recipes` and `recipe_revisions` have a circular FK (`current_revision_id` / `parent_revision_id`). Resolve with `ALTER TABLE` after both tables exist (see existing migration pattern in `0001_init.sql`).

### Community

```sql
CREATE TABLE follows (
  follower_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  followed_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (follower_id, followed_id),
  CHECK (follower_id <> followed_id)
);

CREATE TABLE recipe_comments (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id  UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  author_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON recipe_comments(recipe_id, created_at DESC);

CREATE TABLE batch_comments (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id   UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  author_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON batch_comments(batch_id, created_at DESC);

CREATE TABLE recipe_likes (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipe_id  UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, recipe_id)
);

CREATE TABLE recipe_ratings (
  user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipe_id  UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  rating     SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
  review     TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, recipe_id)
);
```

### Reminders + notifications

```sql
CREATE TABLE recipe_reminder_templates (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id            UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  title                TEXT NOT NULL,
  description          TEXT,
  anchor               reminder_anchor NOT NULL DEFAULT 'pitch',
  offset_minutes       INT NOT NULL,
  suggested_event_kind event_kind,
  sort_order           INT NOT NULL DEFAULT 0
);

CREATE TABLE reminders (
  id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id             UUID REFERENCES batches(id) ON DELETE CASCADE,
  user_id              UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  template_id          UUID REFERENCES recipe_reminder_templates(id),
  title                TEXT NOT NULL,
  description          TEXT,
  fire_at              TIMESTAMPTZ NOT NULL,
  rrule                TEXT,
  status               reminder_status NOT NULL DEFAULT 'scheduled',
  fired_at             TIMESTAMPTZ,
  completed_at         TIMESTAMPTZ,
  suggested_event_kind event_kind,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON reminders(fire_at) WHERE status = 'scheduled';
CREATE INDEX ON reminders(user_id, status);
CREATE INDEX ON reminders(batch_id);

CREATE TABLE notification_prefs (
  user_id           UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  push_enabled      BOOLEAN NOT NULL DEFAULT TRUE,
  email_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
  quiet_hours_start TIME,
  quiet_hours_end   TIME,
  timezone          TEXT NOT NULL DEFAULT 'UTC'
);

CREATE TABLE push_devices (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform     TEXT NOT NULL,
  token        TEXT NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, token)
);

CREATE TABLE notifications (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reminder_id UUID REFERENCES reminders(id) ON DELETE SET NULL,
  kind        TEXT NOT NULL,
  title       TEXT NOT NULL,
  body        TEXT,
  url_path    TEXT,
  read_at     TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON notifications(user_id, created_at DESC) WHERE read_at IS NULL;
```

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

## Useful Queries

```sql
-- Full fork tree under a recipe
WITH RECURSIVE tree AS (
  SELECT id, parent_id, name, author_id, 0 AS depth
    FROM recipes WHERE id = $1
  UNION ALL
  SELECT r.id, r.parent_id, r.name, r.author_id, t.depth + 1
    FROM recipes r JOIN tree t ON r.parent_id = t.id
)
SELECT * FROM tree ORDER BY depth, name;

-- Most-forked recipes (discovery)
SELECT id, name, fork_count FROM recipes
 WHERE visibility = 'public'
 ORDER BY fork_count DESC LIMIT 20;
```

---

## Things to Layer On Later

- Denormalized counters (`likes_count`, `comments_count`) on recipes/batches for feed performance
- Full-text search via `tsvector` + GIN indexes
- Diff view between any two recipe revisions (app-level JSONB compare)
- "Restore to v2" — create new revision from old content
- Reminder digest setting (morning summary vs. individual pings)
- Compare-with-upstream view for forks

---

## Backup + Export (Phase 4)

**Admin backup**: `pg_dump` + media tar, nightly job, local disk or S3-compatible, configurable retention.

**User data export**: async River job, streams JSON + zip (memory-bounded), 7-day TTL, one in-flight per user. Archive includes profile, recipes + revisions, batches + timeline, comments, follows, likes, media.

**User data import**: validates `manifest.schema_version`, creates under importing user's ownership, skips cross-user references. Key decisions: `schema_version` in manifest from day one; account deletion = anonymize not cascade (sentinel user row); encryption required for remote backups.

## Account Deletion (Phase 4)

Anonymize the user record; let them choose per-content-type at deletion time. 30-day grace period with one-click cancel. River job executes at scheduled time.

| Content | Behavior |
|---|---|
| User record | Anonymize: `deleted_at` set, PII nulled, username tombstoned |
| Recipes | User choice (default: keep — forks depend on them) |
| Batches + timeline | User choice (default: delete) |
| Comments | Always anonymize — preserves thread integrity |
| Sessions, devices, follows | Hard delete |

Key decisions: username frozen permanently (identity trust); email nulled (allows re-registration); forks of deleted recipes survive as orphans.

Schema additions needed: `users.deleted_at`, `deletion_scheduled_for`, `deletion_choices JSONB`, `admin_audit_log`.

## Hardware Integration (Phase 5+)

Device adapters are opt-in via build tags — maintainer doesn't own the hardware. Core ships: `devices` + `batch_devices` schema, webhook framework (`POST /api/webhooks/devices/{token}`), `generic_webhook` adapter only.

Key decisions: per-batch calibration offsets (not per-device — drift varies by liquid density); `paused` flag to mute during cold crash without detaching; `device.type` is TEXT not ENUM (plugins need to add types without migrations); `raw_payload` retained for debugging vendor format changes. `zymo-bridge` companion daemon lives in a separate repo.

---

## Open Decisions

- **CLI framework** — stdlib `os.Args` switch for now. Switch to `spf13/cobra` when first subcommand grows a flag or command count passes ~6.

## Non-Goals (For Now)

- **Federation** — no ActivityPub. Instances are islands; data moves via export/import.
- **Hosted SaaS** — AGPL + CLA preserves the option; not the current focus.
- **Native iOS/Android push** — requires Apple/Google relay. Web Push (VAPID) covers it.
