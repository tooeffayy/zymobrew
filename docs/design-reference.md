# Zymo — Reference Design Notes

Overflow from CLAUDE.md — future-phase schemas, useful queries, and deferred feature notes.

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
