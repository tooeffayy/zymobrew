# Zymo — Design Notes

A self-hostable fermentation tracking app. Name from *zymurgy*, the science of fermentation.

## Project Overview

A self-hostable app for tracking fermentation projects, with optional per-instance community features. SaaS deferred.

- **Deployment model**: self-hosted (homelab / VPS / Docker); SaaS deferred
- **Audience**: brewers — solo loggers, family/friends instances, or open public instances
- **Fermentation types** (eventual): mead, beer, cider, wine, kombucha
- **MVP scope**: mead only; expand to other types in later phases
- **Platforms**: web (built into the binary) + mobile (points at instance URL, Mastodon-style)
- **License**: AGPL v3, with a Contributor License Agreement so the project owner can relicense or dual-license commercially in future

## Tech Stack

**Backend (Go)** — ships as a single binary that embeds the compiled web frontend.
- `chi` — HTTP router
- `pgx` + `sqlc` — Postgres driver with type-safe generated query code
- `goose` — schema migrations, run automatically on startup
- `River` — Postgres-native background jobs (transactional enqueue, retries, periodic schedules). **No Redis dependency.**
- Local accounts default; OIDC/OAuth (Google / Apple / Authentik / Authelia) optional via env config

**Frontend**
- Expo + React Native Web — one codebase ships native iOS/Android *and* web
- Mobile app prompts for instance URL on first launch (Mastodon-style)

**Database**
- **Postgres 14+** required
- Schema leans on Postgres features confidently: CITEXT, ENUMs, JSONB + GIN, partial indexes, recursive CTEs, `tsvector` full-text search

**Storage**
- Local disk default; S3-compatible optional. Abstracted behind an interface from day one.

**Notifications**
- In-app inbox (always)
- Email via configurable SMTP
- Web Push via VAPID (no third-party relay needed)
- Native iOS/Android push deferred — requires Apple/Google relay

## Project Layout

```
cmd/zymo/             entry point (serve | migrate | selftest | version)
internal/auth         argon2id password hashing + session token primitives
internal/config       env-based config loader
internal/db           pgx pool + database/sql open helpers
internal/jobs         River client + workers (background jobs, periodic schedules)
internal/migrate      goose runner + River migrator (uses embedded migrations)
internal/queries      sqlc query files + generated type-safe code
internal/ratelimit    in-memory token-bucket limiter (per-IP, per-identifier)
internal/server       chi HTTP router (/healthz, /readyz, /api/auth/*, /api/batches/*)
internal/selftest     runtime smoke tests for `zymo selftest`
internal/testutil     shared DB test setup
migrations/           embedded SQL migrations + embed.go
sqlc.yaml             sqlc generator config
docker-compose.yml    postgres on host port 5433; app service for full-stack run
Dockerfile            multistage distroless build
```

Migrations are baked into the binary via `//go:embed`, so a single binary
ships with everything needed to bootstrap a database on first run.

## Development

```
docker compose up -d postgres
export DATABASE_URL=postgres://zymo:zymo@localhost:5433/zymo?sslmode=disable
go run ./cmd/zymo serve
```

**Subcommands**

| Command         | Purpose |
|-----------------|---------|
| `zymo serve`    | Default. Runs the HTTP server. Auto-migrates unless `AUTO_MIGRATE=false`. |
| `zymo migrate`  | Apply pending migrations and exit. |
| `zymo selftest` | Smoke-check a deployed instance (connect → ping → migration version → schema → CRUD round-trip). Exits non-zero on failure. |
| `zymo version`  | Print build version. |

**Regenerating queries**

`internal/queries/*.sql` files are the source of truth; the matching `*.sql.go`
plus `models.go` / `db.go` / `querier.go` are generated. After editing a `.sql`
file run `sqlc generate` (binary lives in `$(go env GOPATH)/bin/sqlc`; install
with `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`). Generated files
are committed so the project builds without sqlc installed.

**Environment variables**

| Var             | Default       | Notes |
|-----------------|---------------|-------|
| `DATABASE_URL`  | *(required)*  | Postgres connection URL |
| `LISTEN_ADDR`   | `:8080`       | HTTP listen address |
| `INSTANCE_MODE` | `single_user` | `single_user` \| `closed` \| `open` |
| `AUTO_MIGRATE`  | `true`        | Apply pending migrations on `serve` startup |
| `COOKIE_SECURE` | `false`       | Set true in production behind TLS so the session cookie won't be sent over plaintext |

## Tests + Smoke Checks

Two layers, deliberately separate:

1. **`go test ./...`** — unit + integration suite. DB-backed tests skip
   unless `TEST_DATABASE_URL` is set. Run them with:
   ```
   docker compose up -d postgres
   export TEST_DATABASE_URL=postgres://zymo:zymo@localhost:5433/zymo?sslmode=disable
   go test ./...
   ```
   `testutil.Pool(t, ctx)` migrates the schema once per process and returns a
   shared pool; tests isolate via `pool.Begin` + `Rollback`. New DB-backed
   tests just import `zymobrew/internal/testutil` and call it.

2. **`zymo selftest`** — runtime equivalent that runs against a live instance.
   Verifies connectivity, current migration version, expected tables exist,
   and exercises a full CRUD round-trip (users → recipes → revisions →
   batches → readings → events) inside a transaction that rolls back. Use
   after every deploy; wire into healthcheck pipelines if useful.

The two share assumptions intentionally: every check the runtime smoke test
performs is also covered by `go test`, so test failures preview production
failures.

## Licensing

- **AGPL v3** — copyleft with the network-use clause. Anyone running a modified version as a service must publish their source.
- **Contributor License Agreement (CLA)** — contributors grant the project owner the right to relicense their contributions. Preserves the option to dual-license commercially or relicense later.
- **Why this combo**: hobbyist self-hosters are unaffected (they're not redistributing); commercial SaaS forks must either contribute back or buy a commercial license; the CLA keeps the door open for a future hosted offering without losing community trust.
- **Tradeoff accepted**: some companies forbid AGPL code internally, which limits enterprise contributors and integrations. Acceptable since individuals are the primary audience.

---

## Phased Roadmap

1. Auth (local), profile, mead batch CRUD with readings + chart
2. Recipes, forking, instance feed, comments
3. Calculators + reminders / web-push notifications
4. Backup + export (first-class)
5. Cider + wine (reuse ~90% of mead flow)
6. Beer (new flows: mash, boil, IBU)
7. Kombucha (continuous fermentation model — F1/F2)

(See Non-Goals below for items intentionally deferred.)

## Deployment

- Single Go binary OR single Docker image
- Reference `docker-compose.yml` with two services: app + Postgres + a named volume for data
- All config via env vars: DB URL, storage backend, SMTP, OAuth providers, instance mode, registration toggle
- Migrations run automatically on startup
- Backups: nightly JSON export job + on-demand export endpoint

## Instance Modes

Set via env flag at deploy time:

- **Single-user** — registration is closed once a user exists; the *first* registration bootstraps the admin. For solo brewers.
- **Closed** — registration off via the API entirely; users are created out-of-band (CLI bootstrap to come). For family/friends.
- **Open** — anyone can register. For larger public-facing instances.

## Auth

Local accounts only at this stage; OIDC/OAuth will plug in next to the password path.

- **Password storage** — argon2id (m=64MB, t=1, p=4) in PHC string format on `users.password_hash`. Nullable so OIDC users coexist later.
- **Sessions** — opaque random token (32 bytes, URL-safe base64). The SHA-256 of the raw token is what's stored in `sessions.token_hash`; a DB leak does not yield usable tokens. Default lifetime 30 days.
- **Transport** — `Cookie: zymo_session=<token>` for browsers (HttpOnly, SameSite=Lax) **or** `Authorization: Bearer <token>` for native/API clients. Both hit the same `sessions` row, so revocation is one DELETE.
- **Endpoints** — `POST /api/auth/{register,login,logout}`, `GET /api/auth/me`. Register/login return `{token, user}` and Set-Cookie; logout deletes the session row and clears the cookie.
- **Middleware** — `authMiddleware` always runs and stashes the user onto request context if a valid session is present; anonymous requests pass through. `requireAuth` is the route-level gate (returns 401).

**Cookie security** — set `COOKIE_SECURE=true` in production so browsers won't transmit the session cookie over plaintext. Defaults to false for localhost dev. CSRF protection at the moment is SameSite=Lax; CSRF tokens for state-changing requests can come later if/when we host cross-origin frontends.

## Batches API

Authenticated CRUD over the brewer's own batches and the readings timeline that powers the chart. All `/api/batches/*` routes require auth and reject access to other users' rows with 404 (not 403) so existence isn't leaked.

| Method | Path | Purpose |
|---|---|---|
| `POST`   | `/api/batches`                      | Create. Defaults: `brew_type=mead`, `stage=planning`, `visibility=public`. |
| `GET`    | `/api/batches`                      | List the caller's batches (paged via `?offset=N`, 100/page). |
| `GET`    | `/api/batches/{id}`                 | Fetch one. 404 if not owner. |
| `PATCH`  | `/api/batches/{id}`                 | Partial update — name / stage / notes / started_at / bottled_at / visibility. |
| `DELETE` | `/api/batches/{id}`                 | Hard delete. CASCADE clears readings/events/tasting notes. |
| `POST`   | `/api/batches/{id}/readings`        | Add a reading. Requires at least one of gravity / temperature_c / ph. |
| `GET`    | `/api/batches/{id}/readings`        | All readings for a batch, ASC by `taken_at` (chart-friendly). |
| `POST`   | `/api/batches/{id}/events`          | Add a journal event. `kind` required (event_kind enum); `details` optional JSONB object. |
| `GET`    | `/api/batches/{id}/events`          | All events for a batch, ASC by `occurred_at`. |
| `POST`   | `/api/batches/{id}/tasting-notes`   | Add a tasting note. Requires at least one of rating / aroma / flavor / mouthfeel / finish / notes. |
| `GET`    | `/api/batches/{id}/tasting-notes`   | All tasting notes for a batch, DESC by `tasted_at` (newest first). |

**Tasting-note authorship** — `tasting_notes.author_id` is intentionally separate from `batches.brewer_id` so non-owners can leave notes on public batches in phase 2 (community). Phase 1 restricts authoring to the batch owner; the API hard-codes `author_id = current user` after passing the ownership check.

**MVP guard** — the create handler rejects anything other than `brew_type=mead`. The schema/enum support all five brew types; we'll lift the gate as those flows ship.

**PATCH semantics** — uses sqlc's `COALESCE(narg, col)` pattern. Omitted fields stay; sent fields overwrite. **Cannot** clear a column to NULL via PATCH yet. Acceptable for phase 1; revisit if anyone needs it.

**NUMERIC handling** — gravity/temp/pH/ABV are `NUMERIC` in the schema and `pgtype.Numeric` in generated code. Handlers convert via `numericPtr`/`floatToNumeric` helpers (`internal/server/batches.go`). We tried a sqlc override to make NUMERIC → `*float64`, but sqlc doesn't accept Go pointer types in nullable overrides. Float precision is fine for brewing values (3 decimal places).

### Known batch API gaps (deferred)

- **Unbounded list responses** — `GET /api/batches/{id}/{readings,events}` return *all* rows. Manual entry is fine; once Tilt/RAPT adapters land (every-5-minute readings → ~9k rows for a 1-month brew), add LIMIT + cursor pagination.
- **`source` is free text** — schema says `'manual','tilt','rapt'`, etc., but the API accepts whatever string the client sends. Constrain to a known set when device adapters ship.
- **Race window: ownership check + insert** — `userOwnsBatch` then `CreateReading`/`CreateBatchEvent` are two queries. FK CASCADE keeps the data consistent (insert fails or row gets cascaded), but the response can be misleading. Fold into a single `INSERT ... WHERE EXISTS` when motivated.
- **PATCH cannot clear columns to NULL** — covered above. Add per-field "clear" handling when a real workflow needs it.
- **No optimistic concurrency on PATCH** — concurrent edits last-write-wins. Add If-Match / version column when the multi-device story matters.

**Login timing** — the login handler always runs an argon2 verify (against a sentinel hash if the user doesn't exist) so a network observer can't enumerate valid usernames by timing. See `auth.DummyHash` in `internal/auth/password.go`.

## Login rate limiting

Two layers, both in-memory token buckets ([`internal/ratelimit`](internal/ratelimit/ratelimit.go)). State is per-process — fine for the single-replica self-hosted baseline; swap the backing store if multi-replica becomes a thing.

| Layer | Where | Burst | Refill | Keyed by |
|---|---|---|---|---|
| IP gate | middleware on `/api/auth/{register,login}` | 10 | 1 / 2s | `r.RemoteAddr` (post chi `RealIP`) |
| Per-identifier gate | inside `handleLogin` after body decode | 5 | 1 / 12s | `strings.ToLower(req.Identifier)` |

The IP gate trips first under broad floods; the identifier gate catches "single legitimate IP brute-forcing one account". Either trip returns 429 with `Retry-After: 60`.

**Eviction is lazy** — `Allow()` opportunistically prunes idle entries on call. No background goroutine, no `Close()` to remember.

**Trust assumption:** chi's `middleware.RealIP` rewrites `r.RemoteAddr` from `X-Forwarded-For` / `X-Real-IP`. Behind a reverse proxy that's correct; directly exposed, those headers are attacker-controlled and the IP gate is bypassable. Tighten via an explicit trusted-proxy config when we add one.

### Known auth gaps (deferred)

- **Single-user TOCTOU** — bootstrap uses `CountUsers` then `INSERT` in two queries; two simultaneous registrations could both succeed. Realistic only under deliberate racing of a one-time event. Fix when motivated: SERIALIZABLE tx around the check+insert.
- **Rate-limit state is in-process.** Multi-replica deployments will leak some headroom across replicas. Move to a shared store (Redis or a Postgres table evicted via a River job) when we ship multi-replica.
- **No trusted-proxy config.** `middleware.RealIP` blindly trusts `X-Forwarded-For`; the IP rate limit is bypassable for directly-exposed deployments. Add an allowlist when warranted.
- **No `last_seen_at` touch on activity.** `TouchSession` exists but the auth middleware doesn't call it; pick a strategy (every request vs rate-limited update) when the activity feed needs it.
- **No client IP captured on sessions.** `sessions.ip` is `INET NULL` — we'll want to populate it once the trusted-proxy policy is settled.

## Background Jobs

[River](https://riverqueue.com) runs in-process inside the zymo binary — no Redis, no separate worker process. Queue state lives in `river_*` tables alongside the app schema. River applies its own migrations; `migrate.Up` calls goose first, then River's migrator, so a single `zymo migrate` (or auto-migrate at boot) brings the whole DB to a ready state.

**Lifecycle.** `cmd/zymo serve` constructs a `jobs.Client`, starts it after the pgx pool is up, and stops it via deferred call with a fresh 10s context (the parent ctx is already cancelled by then; River needs an unblocked ctx to drain).

**Workers + periodic jobs.** Registered centrally in [`internal/jobs/jobs.go`](internal/jobs/jobs.go); each worker lives in its own file alongside its `Args` struct.

| Job | Schedule | Purpose |
|---|---|---|
| `expired_sessions_gc` | every hour, `RunOnStart: true` | `DELETE FROM sessions WHERE expires_at < now()`. Idempotent. |

**Testing pattern.** Drive workers directly without the River runtime — construct a synthetic `*river.Job[Args]`, call `Work(ctx, job)`, assert the side effect. See [`expired_sessions_test.go`](internal/jobs/expired_sessions_test.go).

**Adding a job.**
1. New file `internal/jobs/<name>.go` with `<Name>Args` (with `Kind() string`) and `<name>Worker` (embedding `river.WorkerDefaults[<Name>Args]`, holding any state it needs).
2. Register the worker with `river.AddWorker(workers, ...)` inside `New()`.
3. If periodic, add a `river.NewPeriodicJob(...)` entry to `Config.PeriodicJobs`.
4. Test the worker function directly against `testutil.Pool`.

**Schema-count tripwire.** [`internal/db/db_test.go`](internal/db/db_test.go) excludes `river_%` tables from its count so River version bumps don't false-positive the test. Selftest, by contrast, *does* assert `river_job` and `river_migration` exist — that's how we verify the River migrator ran end-to-end after deploy.

## Core Concepts

- A **batch is a timeline**. The recipe defines the plan; the batch records what actually happened.
- **Readings** (gravity, temp, pH) are quantitative timeline data — power the chart.
- **Events** (rack, dry hop, bottle) are qualitative timeline data — power the journal.
- **Tasting notes** are first-class because they're rich, structured, and revisited as the brew ages.
- **Recipes are forkable** with full revision history; batches pin to the exact recipe revision they were brewed from.

---

## Database Schema

### Enums

```sql
CREATE TYPE brew_type AS ENUM ('mead','beer','cider','wine','kombucha');
CREATE TYPE visibility AS ENUM ('public','unlisted','private');
CREATE TYPE batch_stage AS ENUM
  ('planning','primary','secondary','aging','bottled','archived');
CREATE TYPE ingredient_kind AS ENUM
  ('honey','water','yeast','nutrient','fruit','spice','oak','acid','tannin','other');
CREATE TYPE event_kind AS ENUM
  ('pitch','nutrient_addition','degas','rack','addition',
   'stabilize','backsweeten','bottle','photo','note','other');
CREATE TYPE reminder_status AS ENUM
  ('scheduled','fired','completed','snoozed','dismissed','cancelled');
CREATE TYPE reminder_anchor AS ENUM
  ('absolute','batch_start','pitch','rack','bottle','custom_event');
```

### Users

```sql
CREATE TABLE users (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username     CITEXT UNIQUE NOT NULL,
  email        CITEXT UNIQUE NOT NULL,
  display_name TEXT,
  bio          TEXT,
  avatar_url   TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

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
  style               TEXT,                 -- 'traditional','melomel','cyser'...
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
  details    JSONB NOT NULL DEFAULT '{}'   -- {varietal:'orange blossom'} etc
);
CREATE INDEX ON recipe_ingredients(recipe_id);

-- Full edit history. Each save = one revision with a JSONB snapshot.
CREATE TABLE recipe_revisions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id       UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  revision_number INT  NOT NULL,           -- 1, 2, 3... per recipe
  author_id       UUID NOT NULL REFERENCES users(id),
  message         TEXT,                    -- commit-style: "Bumped honey to 2kg"
  name         TEXT NOT NULL,
  description  TEXT,
  style        TEXT,
  target_og    NUMERIC(5,3),
  target_fg    NUMERIC(5,3),
  target_abv   NUMERIC(4,2),
  batch_size_l NUMERIC(6,2),
  ingredients  JSONB NOT NULL,             -- snapshot of ingredients
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (recipe_id, revision_number)
);
CREATE INDEX ON recipe_revisions(recipe_id, revision_number DESC);
CREATE INDEX ON recipe_revisions(author_id);
```

### Batches (the actual brew)

```sql
CREATE TABLE batches (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  brewer_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  recipe_id          UUID REFERENCES recipes(id) ON DELETE SET NULL,
  recipe_revision_id UUID REFERENCES recipe_revisions(id),
  name               TEXT NOT NULL,
  brew_type          brew_type NOT NULL,
  stage              batch_stage NOT NULL DEFAULT 'planning',
  started_at         TIMESTAMPTZ,
  bottled_at         TIMESTAMPTZ,
  visibility         visibility NOT NULL DEFAULT 'public',
  notes              TEXT,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON batches(brewer_id);
CREATE INDEX ON batches(stage);

CREATE TABLE batch_ingredients (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id   UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  kind       ingredient_kind NOT NULL,
  name       TEXT NOT NULL,
  amount     NUMERIC(10,3),
  unit       TEXT,
  sort_order INT NOT NULL DEFAULT 0,
  details    JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX ON batch_ingredients(batch_id);
```

### Timeline: readings + events + tasting notes

```sql
-- READINGS: quantitative timeline (the chart)
CREATE TABLE readings (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id      UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  taken_at      TIMESTAMPTZ NOT NULL,
  gravity       NUMERIC(5,3),
  temperature_c NUMERIC(5,2),
  ph            NUMERIC(4,2),
  notes         TEXT,
  source        TEXT NOT NULL DEFAULT 'manual'  -- 'manual','tilt','rapt'
);
CREATE INDEX ON readings(batch_id, taken_at DESC);

-- EVENTS: qualitative timeline (the journal)
CREATE TABLE batch_events (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id    UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  occurred_at TIMESTAMPTZ NOT NULL,
  kind        event_kind NOT NULL,
  title       TEXT,
  description TEXT,
  details     JSONB NOT NULL DEFAULT '{}'   -- {product:'Fermaid-O',amount_g:1.5}
);
CREATE INDEX ON batch_events(batch_id, occurred_at DESC);

-- Photos / media on events (normalized for captions + ordering)
CREATE TABLE batch_event_media (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id   UUID NOT NULL REFERENCES batch_events(id) ON DELETE CASCADE,
  url        TEXT NOT NULL,
  caption    TEXT,
  sort_order INT NOT NULL DEFAULT 0
);
CREATE INDEX ON batch_event_media(event_id, sort_order);

CREATE TABLE tasting_notes (
  id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id  UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  author_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tasted_at TIMESTAMPTZ NOT NULL,
  rating    SMALLINT CHECK (rating BETWEEN 1 AND 5),
  aroma     TEXT,
  flavor    TEXT,
  mouthfeel TEXT,
  finish    TEXT,
  notes     TEXT
);
CREATE INDEX ON tasting_notes(batch_id, tasted_at DESC);
```

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
-- Reminder TEMPLATES on a recipe (auto-materialize when batch starts)
CREATE TABLE recipe_reminder_templates (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id       UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  description     TEXT,
  anchor          reminder_anchor NOT NULL DEFAULT 'pitch',
  offset_minutes  INT NOT NULL,                -- 1440 = 24h after anchor
  suggested_event_kind event_kind,             -- so "Done" logs the right batch event
  sort_order      INT NOT NULL DEFAULT 0
);

-- Concrete reminders on a batch
CREATE TABLE reminders (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id     UUID REFERENCES batches(id) ON DELETE CASCADE,
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  template_id  UUID REFERENCES recipe_reminder_templates(id),
  title        TEXT NOT NULL,
  description  TEXT,
  fire_at      TIMESTAMPTZ NOT NULL,
  rrule        TEXT,                        -- iCal RRULE for recurring
  status       reminder_status NOT NULL DEFAULT 'scheduled',
  fired_at     TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  suggested_event_kind event_kind,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ON reminders(fire_at) WHERE status = 'scheduled';
CREATE INDEX ON reminders(user_id, status);
CREATE INDEX ON reminders(batch_id);

CREATE TABLE notification_prefs (
  user_id      UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  push_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
  email_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  quiet_hours_start TIME,
  quiet_hours_end   TIME,
  timezone     TEXT NOT NULL DEFAULT 'UTC'
);

CREATE TABLE push_devices (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  platform     TEXT NOT NULL,             -- 'ios','android','web'
  token        TEXT NOT NULL,
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, token)
);

-- Universal notification feed (reminders, comments, follows, forks, etc.)
CREATE TABLE notifications (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reminder_id UUID REFERENCES reminders(id) ON DELETE SET NULL,
  kind        TEXT NOT NULL,             -- 'reminder','comment','fork','follow'
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
- **Recipe vs batch ingredients are separate tables** — recipe is the plan, batch records what was actually used. Substituting a honey doesn't mutate the recipe.
- **Hybrid normalized + JSONB ingredients** — common columns for querying/aggregation; `details` JSONB for kind-specific fields (yeast strain, fruit prep). New ingredient kinds = enum value, no schema migration.
- **Readings vs events split** — readings power the chart with one fast indexed query; events are the journal. Combining hurts both.
- **Tasting notes first-class** — rich repeated structure; brewers add multiple as the brew ages.
- **Forking via `parent_id`** — simple lineage, fully walkable.
- **`source` on readings** — future-proofs hardware integration (Tilt/RAPT) with no schema change.
- **`brew_type` denormalized on batches** — filters without joining to recipe; freeform batches have no recipe.
- **Split comments/likes per type** — small duplication, but FK integrity and trivial queries beat polymorphic associations.

### Recipe revisions
- **JSONB ingredient snapshots** — revisions are immutable history, not relational query targets. JSONB keeps it one row per revision.
- **`current_revision_id`** — O(1) HEAD lookup, no `MAX(revision_number)` scan.
- **Batches pin to `recipe_revision_id`** — six months later the recipe author tweaks honey amounts; old batch logs still reflect what was actually brewed.
- **`parent_revision_id` on recipes** — fork lineage points at a specific version, not floating HEAD.
- **Per-recipe revision numbers** — readable URLs (`/recipes/orange-mead/v3`) instead of UUIDs.
- **Revisions created in service layer**, not by trigger — easier to control *when* a snapshot warrants saving (don't snapshot every keystroke autosave).

### Forking model
- **Fork = new recipe with `parent_id` + `parent_revision_id`** — pins exact version, copies ingredients to a fresh row set.
- **Fork operation in single transaction**, increments `fork_count` on source.
- **Visibility rules**: cannot fork private; can fork public or unlisted.
- **Allow forking own recipes** — common experimentation pattern.
- **Allow re-forking forks** — full lineage chain preserved.
- **No "merge upstream" for v1** — brewing recipes aren't bug-fixed; forkers don't generally want author changes auto-applied.
- **Likes/comments/ratings independent per recipe** — clean social break.
- **Don't force "(fork)" in name** — "Forked from @user/recipe" attribution is cleaner.

### Reminders
- **Reminder templates on recipes** materialize into concrete `reminders` when a batch starts; re-resolve on stage transitions (pitch, rack, bottle) so the schedule self-builds as the batch progresses.
- **DB poll dispatcher** (every minute) — chosen over queue-at-creation because reminders get edited/cancelled frequently (rack early → "Day 14 rack" reminder dies).
- **Atomic claim** before dispatch prevents double-send across workers.
- **In-app notifications always created**; push/email gated by prefs — gives a reliable inbox even when devices are silenced.
- **Quiet hours respect user timezone** — store TZ on prefs, never compute against UTC.
- **"Mark done" notification action** creates the suggested batch event in one tap.
- **Smart reminders** (no template needed):
  - No reading in N days during primary → "Time for a gravity check?"
  - Gravity stable across 3+ readings (Δ < 0.002) → "Looks done — consider racking"
  - Stage transition to `aging` → auto-schedule 1mo / 3mo / 6mo / 1yr milestones
  - Backsweetened without stabilizer logged → safety nudge
- **Snooze options**: 1h / tomorrow / next weekend.
- **Per-batch mute** — for long-aged meads you don't want nagging.

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
SELECT id, name, fork_count
  FROM recipes
 WHERE visibility = 'public'
 ORDER BY fork_count DESC LIMIT 20;
```

---

## Things to Layer On Later

- Denormalized counters (`likes_count`, `comments_count`) on recipes/batches for feed performance
- Full-text search via `tsvector` columns + GIN indexes
- Soft delete (`deleted_at`) once moderation needs arise
- Diff view between any two recipe revisions (app-level, comparing JSONB)
- "Restore to v2" — create new revision with v2's content
- "Brewed from v3" badges on batches
- Reminder digest setting (morning summary instead of individual pings)
- Compare-with-upstream view for forks

---

## Backup + Export

Two distinct features. Both first-class.

**Admin backup** (operator disaster recovery)
- `pg_dump --format=custom` + `tar` of media directory, combined into a timestamped archive
- Nightly job, configurable retention (keep last N)
- Local disk default; S3-compatible optional with at-rest encryption (admin passphrase)
- Admin UI: download latest / run now
- Restore: documented manual procedure (`pg_restore` + extract tarball)

**User data export** (portable per-user archive)
- All data the user owns: profile, recipes (+ all revisions), batches (+ readings, events, tasting notes), comments authored, follows, likes, ratings
- Owned media included as files
- Async job (River); streams JSON + zip to keep memory bounded
- TTL on completed exports (7 days default); cleanup job removes expired ones
- One in flight per user (rate limit)

Archive layout:

```
export-{user}-{date}.zip
├── manifest.json          (schema_version, exported_at, user info)
├── profile.json
├── recipes/{id}/
│   ├── recipe.json
│   └── revisions/{n}.json
├── batches/{id}/
│   ├── batch.json
│   ├── readings.json
│   ├── events.json
│   └── tasting_notes.json
├── comments.json
├── follows.json, likes.json, ratings.json
└── media/                 (original files; relative paths from JSON)
```

**User data import** (inverse of export)
- Validates `manifest.schema_version`; migrates older exports forward
- Creates everything under importing user's ownership
- Cross-user references (comments others left, followers) skipped — privacy
- Forks: if `parent_id` exists on this instance, link it; otherwise import standalone with attribution preserved in description
- Returns a summary: "Imported 12 recipes, 8 batches, 47 photos. Skipped 3 comments by other users."

**Schema**

```sql
CREATE TYPE job_status AS ENUM ('pending','running','complete','failed','expired');

CREATE TABLE user_exports (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status       job_status NOT NULL DEFAULT 'pending',
  file_path    TEXT,
  size_bytes   BIGINT,
  error        TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ,
  expires_at   TIMESTAMPTZ
);
CREATE INDEX ON user_exports(user_id, created_at DESC);

CREATE TABLE user_imports (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status          job_status NOT NULL DEFAULT 'pending',
  source_filename TEXT,
  summary         JSONB,
  error           TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at    TIMESTAMPTZ
);

CREATE TABLE admin_backups (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  status          job_status NOT NULL DEFAULT 'pending',
  file_path       TEXT,
  size_bytes      BIGINT,
  storage_backend TEXT NOT NULL,           -- 'local','s3'
  error           TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at    TIMESTAMPTZ
);
```

**Design decisions**

- **`schema_version` in manifest from day one** — lets future versions migrate exports made by old versions.
- **Skip other users' content in your export** — privacy; they don't belong to the exporter.
- **Account deletion = anonymize, not cascade** — replace `author_id` with a "deleted user" sentinel so comment threads on shared recipes don't break for everyone else. Worth deciding alongside this feature; affects schema (a `users.deleted_at` flag and a sentinel user row).
- **Encryption for remote backups** — admin-configured passphrase, required for restore.
- **Rate-limit exports** — one job in flight per user; large zips are expensive.

---

## Account Deletion

The hardest call: what "delete" means for content with social context. Hard-cascade breaks other users' data; pure anonymize denies the user agency. Solution: anonymize the user record, let them choose per-content-type at deletion time.

**Per-content behavior**

| Content | Behavior |
|---|---|
| User record | Anonymize: `deleted_at` set, PII nulled, username locked as tombstone, login destroyed |
| Recipes | User choice. Default = keep (forks depend on them). Delete leaves forks as orphans via existing FK |
| Batches + readings + events + tasting notes | User choice. Default = delete (personal records) |
| Comments | Always anonymize — preserves thread integrity |
| Likes / ratings | Anonymize, keep counts |
| Follows, push devices, sessions, prefs, exports/imports, OAuth tokens | Hard delete |

**Deletion flow**

1. User clicks Delete Account
2. Re-authenticate + confirm
3. Offered "Download my data" (links to user export)
4. Per-content-type choices captured into `deletion_choices` JSONB
5. `deletion_scheduled_for = now() + 30 days`
6. 30-day grace period: login works, banner shows "Scheduled for X. [Cancel]"
7. At scheduled time, River job executes deletion transactionally
8. User notified via email; further login impossible

Cancellation is a one-click button during grace.

**Schema additions**

```sql
ALTER TABLE users
  ADD COLUMN deleted_at             TIMESTAMPTZ,
  ADD COLUMN deletion_scheduled_for TIMESTAMPTZ,
  ADD COLUMN deletion_choices       JSONB,  -- {keep_recipes:true, keep_batches:false}
  ADD COLUMN deletion_reason        TEXT;   -- optional self-report

CREATE INDEX ON users(deletion_scheduled_for)
  WHERE deletion_scheduled_for IS NOT NULL AND deleted_at IS NULL;

-- All "find active user" queries: WHERE deleted_at IS NULL
-- FKs continue to resolve — the row persists, just tombstoned

CREATE TABLE admin_audit_log (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  admin_id    UUID NOT NULL REFERENCES users(id),
  action      TEXT NOT NULL,        -- 'delete_user', 'force_delete_recipe', etc.
  target_type TEXT NOT NULL,
  target_id   UUID NOT NULL,
  reason      TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**Design decisions**

- **Username freezing** — locked permanently; prevents identity confusion (Twitter, GitHub pattern). Slowly pollutes namespace but worth the trust gain.
- **Email reuse** — null the email field on tombstone so the same address can register fresh.
- **Backups** — deleted data persists in admin backups until retention rotates out. Document honestly in privacy docs; no special purge.
- **Forks of deleted recipes** — survive as orphans, UI shows "Forked from a deleted recipe." The `parent_revision_id` snapshot preserves the original content if forks need it.
- **Admin-initiated deletion** — same machinery, optional skip-grace flag for abuse cases, always logged to `admin_audit_log`.
- **Anonymize-by-default for comments** — comment threads on shared recipes don't break for everyone else when one participant leaves.

---

## Hardware Integration (Opt-In Plugin Architecture)

Device adapters are **not enabled by default**. The maintainer doesn't own the hardware to verify functionality, so each device type ships as a community-contributed plugin gated behind a build tag. Core stays clean and tested; adapters live alongside maintainers who actually have the equipment.

**What ships in default core**
- The `devices` + `batch_devices` schema
- The webhook framework (`POST /api/webhooks/devices/{token}`)
- A single `generic_webhook` adapter — pass-through that stores raw JSON, lets anyone with a custom script integrate today
- The adapter registry, empty by default

**What does NOT ship in default core**
- Tilt, RAPT, iSpindel, Plaato, Brewfather adapters — all opt-in via build tags
- `zymo-bridge` companion daemon — separate project, separate repo

**Adapter interface**

```go
type DeviceAdapter interface {
    Type() string                          // 'tilt','rapt_pill', etc.
    Parse(raw []byte) (Reading, error)
    Validate(cfg map[string]any) error
}

type Reading struct {
    TakenAt     time.Time
    Gravity     *float64
    Temperature *float64
    PH          *float64
}

var registry = map[string]DeviceAdapter{}
func Register(a DeviceAdapter) { registry[a.Type()] = a }
```

**Build-tag gating**

```go
//go:build adapter_tilt
package adapters
func init() { device.Register(&TiltAdapter{}) }
```

- Default build (`go build`) — generic webhook only
- Full build (`go build -tags "adapter_tilt adapter_rapt adapter_ispindel"`) — opt in
- Docker: `zymo:latest` (core), `zymo:latest-extras` (community adapters)

**The `batch_devices` model**

A device outlives any single batch — you buy one Tilt and use it across dozens of brews over years. So:

- `devices` = the physical thing you own (one row per sensor)
- `batches` = a brew (one row per fermentation run)
- `batch_devices` = link table recording "this device was attached to this batch from time X to time Y"

Two non-obvious fields:

- **Calibration offsets live here, not on the device.** Tilts drift, and the offset that makes one accurate in a thin mead won't suit a thick wort. Per-batch offsets let each brew have its own correction without reflashing the device or affecting other batches.
- **`paused`** — during cold crash, dry hop, or transfer, readings go haywire. Detaching loses the association entirely; pausing just mutes ingestion temporarily.

Concrete example:

```
devices:        "Red Tilt"  (bought 2023)

batches:        "Spring Mead 2024"   (Mar–May 2024)
                "Summer Cyser"       (Jun–Aug 2024)
                "Holiday Braggot"    (Oct 2024 – ongoing)

batch_devices:  Red Tilt → Spring Mead    (Mar 1 → May 15, offset -0.003)
                Red Tilt → Summer Cyser   (Jun 1 → Aug 20, offset -0.002)
                Red Tilt → Holiday Braggot (Oct 5 → NULL,  offset -0.001, paused during cold crash)
```

One device, three batches, three calibrations, full history. The webhook handler uses the row with `detached_at IS NULL` to route incoming readings.

**Schema (revised — no ENUM)**

```sql
-- device.type is TEXT, validated at runtime against the adapter registry.
-- ENUMs require migrations to add values, which fights the plugin model.

CREATE TABLE devices (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  type          TEXT NOT NULL,                  -- runtime-validated against registry
  config        JSONB NOT NULL DEFAULT '{}',
  webhook_token TEXT UNIQUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ
);
CREATE INDEX ON devices(user_id);
CREATE INDEX ON devices(webhook_token) WHERE webhook_token IS NOT NULL;

CREATE TABLE batch_devices (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id           UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  device_id          UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  attached_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  detached_at        TIMESTAMPTZ,
  gravity_offset     NUMERIC(5,3) NOT NULL DEFAULT 0,
  temperature_offset NUMERIC(4,2) NOT NULL DEFAULT 0,
  paused             BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE INDEX ON batch_devices(batch_id) WHERE detached_at IS NULL;
CREATE INDEX ON batch_devices(device_id);

ALTER TABLE readings
  ADD COLUMN device_id   UUID REFERENCES devices(id) ON DELETE SET NULL,
  ADD COLUMN raw_payload JSONB;
```

**Webhook ingestion flow**

```
POST /api/webhooks/devices/{token}
  → look up device by token
  → reject if no active batch_devices row, or paused
  → registry[device.type].Parse(body) → {gravity, temp_c, ph, taken_at}
  → apply batch_devices calibration offsets
  → outlier filter (gravity 0.980–1.200, temp –10–50°C)
  → INSERT reading (device_id, source = device.type, raw_payload)
  → bump devices.last_seen_at
  → 200 OK
```

**Settings UX**
- "Devices" page only appears in nav if at least one adapter beyond `generic_webhook` is registered
- Notice on page: *"Device adapters are community-maintained. Enable in your build / config to add specific devices."*
- Generic webhook always available with example curl + JSON schema — escape hatch for any custom script

**Contribution policy**
- `/docs/integrations/CONTRIBUTING.md` — how to write an adapter
- **Adapters merged only when accompanied by a maintainer who owns the hardware** + recorded payload fixtures in `testdata/`
- Anyone can verify parser correctness against fixtures without owning the device
- Adapters without active maintainers get marked deprecated, eventually removed

**Design decisions**
- **Per-batch calibration** on `batch_devices` — correct drift without reflashing.
- **`paused` flag** — mute during cold crash / dry hop / transfer without detaching.
- **Outlier rejection** — hard plausibility bounds; outliers logged, not stored.
- **`raw_payload` retained** — invaluable for debugging vendor format changes.
- **Last-seen monitoring** — alert during active fermentation if device silent for N hours.
- **No ENUM for `device.type`** — plugin model needs to add types without migrations.
- **`zymo-bridge` lives in a separate repo** — different release cadence, different platform constraints (Pi/embedded), maintained by whoever owns the hardware.

---

## Open Decisions

- **CLI framework** — currently stdlib `os.Args` switch in `cmd/zymo/main.go`. Switch to `spf13/cobra` when the *first* of these triggers: (a) any subcommand grows a flag (`zymo migrate down --to=N`, `zymo backup create --output=path`), or (b) command count passes ~6. Until then the switch is shorter and dep-free.

## Non-Goals (For Now)

- **Federation between instances** — no ActivityPub, no cross-instance follows or recipe sharing. Instances are islands; users move data via export/import. Revisit if/when the project has multiple active instances and demand exists.
- **Hosted SaaS** — self-hosting is the primary distribution model. AGPL + CLA preserves the option to add this later.
- **Native iOS/Android push notifications** — requires Apple/Google relay infrastructure that doesn't fit self-hosted by default. Web Push (VAPID) covers the gap.
