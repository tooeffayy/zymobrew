-- +goose Up

-- custom_event reminder anchor: a template with anchor='custom_event' fires
-- relative to a batch event of a chosen kind. The reminder_anchor enum already
-- carried 'custom_event' (0001_init) but materialization was deferred pending a
-- selector for *which* event kind to anchor to. This column is that selector:
-- only consulted when anchor='custom_event', NULL for every other anchor.
ALTER TABLE recipe_reminder_templates
  ADD COLUMN custom_event_kind event_kind;

-- +goose Down

ALTER TABLE recipe_reminder_templates
  DROP COLUMN custom_event_kind;
