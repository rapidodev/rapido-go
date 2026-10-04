-- +goose Up
-- Per-category forum-topic ids within the one logger group/channel, so a
-- noisy event type (a reseller bot logging in before every call, a routine
-- user creation) lands in its own thread instead of every notification
-- type piling into one feed. Keyed by the same category strings
-- internal/report.Dispatcher passes to each notification method; a
-- category missing from this map just posts to the group's General topic.
ALTER TABLE integration_settings ADD COLUMN telegram_topic_ids JSONB;

-- +goose Down
ALTER TABLE integration_settings DROP COLUMN telegram_topic_ids;
