-- +goose Up
-- 00032 added last_usage_reset_at empty, and the job it arrived with falls back to
-- created_at, so the first pass after that update saw every user on a reset
-- strategy older than one period as due: it zeroed their usage and reactivated the
-- limited ones, at 500 users a minute. This gives those users their anchor before
-- any job runs, so a period starts now instead of one being declared over. Younger
-- users keep created_at, which is right for them, and users already reset have one.
UPDATE users SET last_usage_reset_at = now()
WHERE last_usage_reset_at IS NULL
  AND data_limit_reset_strategy != 'no_reset'
  AND created_at <= now() - CASE data_limit_reset_strategy
      WHEN 'day' THEN interval '1 day'
      WHEN 'week' THEN interval '1 week'
      WHEN 'month' THEN interval '1 month'
      WHEN 'year' THEN interval '1 year'
      ELSE interval '1000 years'
  END;

-- +goose Down
-- Nothing to undo: an anchor set here means the same as one set by a reset.
