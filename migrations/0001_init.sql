-- +goose Up

CREATE EXTENSION IF NOT EXISTS citext;

-- =============================================================================
-- Enums
-- =============================================================================

CREATE TYPE brew_type AS ENUM ('mead','beer','cider','wine','kombucha');
CREATE TYPE visibility AS ENUM ('public','unlisted','private');
CREATE TYPE batch_stage AS ENUM ('planning','primary','secondary','aging','bottled','archived');
CREATE TYPE ingredient_kind AS ENUM ('honey','water','yeast','nutrient','fruit','spice','oak','acid','tannin','other');
CREATE TYPE event_kind AS ENUM ('pitch','nutrient_addition','degas','rack','addition','stabilize','backsweeten','bottle','photo','note','other');
CREATE TYPE reminder_status AS ENUM ('scheduled','fired','completed','snoozed','dismissed','cancelled');
CREATE TYPE reminder_anchor AS ENUM ('absolute','batch_start','pitch','rack','bottle','custom_event');
CREATE TYPE job_status AS ENUM ('pending','running','complete','failed','expired');

-- =============================================================================
-- Users (deletion fields folded in — see CLAUDE.md "Account Deletion")
-- =============================================================================

CREATE TABLE users (
  id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username               CITEXT UNIQUE NOT NULL,
  email                  CITEXT UNIQUE NOT NULL,
  display_name           TEXT,
  bio                    TEXT,
  avatar_url             TEXT,
  deleted_at             TIMESTAMPTZ,
  deletion_scheduled_for TIMESTAMPTZ,
  deletion_choices       JSONB,
  deletion_reason        TEXT,
  created_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX users_deletion_scheduled_idx ON users(deletion_scheduled_for)
  WHERE deletion_scheduled_for IS NOT NULL AND deleted_at IS NULL;

-- =============================================================================
-- Recipes + revisions + ingredients
-- Circular FK: recipes references recipe_revisions(id) for current/parent
-- revision pointers; resolved with ALTER TABLE after both tables exist.
-- =============================================================================

CREATE TABLE recipes (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  author_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_id           UUID REFERENCES recipes(id) ON DELETE SET NULL,
  parent_revision_id  UUID,
  current_revision_id UUID,
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
CREATE INDEX recipes_author_idx       ON recipes(author_id);
CREATE INDEX recipes_parent_idx       ON recipes(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX recipes_type_visibility  ON recipes(brew_type, visibility);

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
CREATE INDEX recipe_ingredients_recipe_idx ON recipe_ingredients(recipe_id);

CREATE TABLE recipe_revisions (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  recipe_id       UUID NOT NULL REFERENCES recipes(id) ON DELETE CASCADE,
  revision_number INT  NOT NULL,
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
CREATE INDEX recipe_revisions_recipe_idx ON recipe_revisions(recipe_id, revision_number DESC);
CREATE INDEX recipe_revisions_author_idx ON recipe_revisions(author_id);

ALTER TABLE recipes
  ADD CONSTRAINT recipes_parent_revision_fkey
    FOREIGN KEY (parent_revision_id) REFERENCES recipe_revisions(id),
  ADD CONSTRAINT recipes_current_revision_fkey
    FOREIGN KEY (current_revision_id) REFERENCES recipe_revisions(id);

-- =============================================================================
-- Batches + ingredients
-- =============================================================================

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
CREATE INDEX batches_brewer_idx ON batches(brewer_id);
CREATE INDEX batches_stage_idx  ON batches(stage);

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
CREATE INDEX batch_ingredients_batch_idx ON batch_ingredients(batch_id);

-- =============================================================================
-- Devices (declared before readings so readings.device_id FK resolves)
-- =============================================================================

CREATE TABLE devices (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  type          TEXT NOT NULL,
  config        JSONB NOT NULL DEFAULT '{}',
  webhook_token TEXT UNIQUE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ
);
CREATE INDEX devices_user_idx          ON devices(user_id);
CREATE INDEX devices_webhook_token_idx ON devices(webhook_token) WHERE webhook_token IS NOT NULL;

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
CREATE INDEX batch_devices_active_idx ON batch_devices(batch_id) WHERE detached_at IS NULL;
CREATE INDEX batch_devices_device_idx ON batch_devices(device_id);

-- =============================================================================
-- Timeline: readings, events, event media, tasting notes
-- (readings.device_id + raw_payload folded in from CLAUDE.md hardware section)
-- =============================================================================

CREATE TABLE readings (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id      UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  device_id     UUID REFERENCES devices(id) ON DELETE SET NULL,
  taken_at      TIMESTAMPTZ NOT NULL,
  gravity       NUMERIC(5,3),
  temperature_c NUMERIC(5,2),
  ph            NUMERIC(4,2),
  notes         TEXT,
  source        TEXT NOT NULL DEFAULT 'manual',
  raw_payload   JSONB
);
CREATE INDEX readings_batch_taken_idx ON readings(batch_id, taken_at DESC);

CREATE TABLE batch_events (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id    UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  occurred_at TIMESTAMPTZ NOT NULL,
  kind        event_kind NOT NULL,
  title       TEXT,
  description TEXT,
  details     JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX batch_events_batch_occurred_idx ON batch_events(batch_id, occurred_at DESC);

CREATE TABLE batch_event_media (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id   UUID NOT NULL REFERENCES batch_events(id) ON DELETE CASCADE,
  url        TEXT NOT NULL,
  caption    TEXT,
  sort_order INT NOT NULL DEFAULT 0
);
CREATE INDEX batch_event_media_event_idx ON batch_event_media(event_id, sort_order);

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
CREATE INDEX tasting_notes_batch_idx ON tasting_notes(batch_id, tasted_at DESC);

-- =============================================================================
-- Community: follows, comments, likes, ratings
-- =============================================================================

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
CREATE INDEX recipe_comments_recipe_idx ON recipe_comments(recipe_id, created_at DESC);

CREATE TABLE batch_comments (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  batch_id   UUID NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  author_id  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  body       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX batch_comments_batch_idx ON batch_comments(batch_id, created_at DESC);

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

-- =============================================================================
-- Reminders + notifications + push
-- =============================================================================

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
CREATE INDEX recipe_reminder_templates_recipe_idx ON recipe_reminder_templates(recipe_id);

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
CREATE INDEX reminders_fire_at_idx     ON reminders(fire_at) WHERE status = 'scheduled';
CREATE INDEX reminders_user_status_idx ON reminders(user_id, status);
CREATE INDEX reminders_batch_idx       ON reminders(batch_id);

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
CREATE INDEX notifications_unread_idx ON notifications(user_id, created_at DESC) WHERE read_at IS NULL;

-- =============================================================================
-- Backup / export / import
-- =============================================================================

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
CREATE INDEX user_exports_user_idx ON user_exports(user_id, created_at DESC);

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
  storage_backend TEXT NOT NULL,
  error           TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at    TIMESTAMPTZ
);

-- =============================================================================
-- Admin audit log
-- =============================================================================

CREATE TABLE admin_audit_log (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  admin_id    UUID NOT NULL REFERENCES users(id),
  action      TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id   UUID NOT NULL,
  reason      TEXT,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- +goose Down

DROP TABLE IF EXISTS admin_audit_log;
DROP TABLE IF EXISTS admin_backups;
DROP TABLE IF EXISTS user_imports;
DROP TABLE IF EXISTS user_exports;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS push_devices;
DROP TABLE IF EXISTS notification_prefs;
DROP TABLE IF EXISTS reminders;
DROP TABLE IF EXISTS recipe_reminder_templates;
DROP TABLE IF EXISTS recipe_ratings;
DROP TABLE IF EXISTS recipe_likes;
DROP TABLE IF EXISTS batch_comments;
DROP TABLE IF EXISTS recipe_comments;
DROP TABLE IF EXISTS follows;
DROP TABLE IF EXISTS tasting_notes;
DROP TABLE IF EXISTS batch_event_media;
DROP TABLE IF EXISTS batch_events;
DROP TABLE IF EXISTS readings;
DROP TABLE IF EXISTS batch_devices;
DROP TABLE IF EXISTS devices;
DROP TABLE IF EXISTS batch_ingredients;
DROP TABLE IF EXISTS batches;
ALTER TABLE recipes
  DROP CONSTRAINT IF EXISTS recipes_current_revision_fkey,
  DROP CONSTRAINT IF EXISTS recipes_parent_revision_fkey;
DROP TABLE IF EXISTS recipe_revisions;
DROP TABLE IF EXISTS recipe_ingredients;
DROP TABLE IF EXISTS recipes;
DROP TABLE IF EXISTS users;

DROP TYPE IF EXISTS job_status;
DROP TYPE IF EXISTS reminder_anchor;
DROP TYPE IF EXISTS reminder_status;
DROP TYPE IF EXISTS event_kind;
DROP TYPE IF EXISTS ingredient_kind;
DROP TYPE IF EXISTS batch_stage;
DROP TYPE IF EXISTS visibility;
DROP TYPE IF EXISTS brew_type;
