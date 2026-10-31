-- +goose Up
-- The first pass of the periodic reset job (see 00036) zeroed every user on a reset
-- strategy who was older than one period. This hands that usage back, but only where
-- the row is still the last word on the user's counter:
--   * it is their only reset since 00032 ran, so no plan or admin reset came after it
--   * their anchor is still its timestamp, so no later pass ran (one statement wrote
--     both, so they are equal to the microsecond)
--   * they were older than a period by then, or it was a real reset, not the false one
--   * the period it opened is still running, as one that has ended clears this anyway
--   * it belongs to the sweep, which took 500 users a minute from its first run. A lone
--     reset days later is an admin's, of a user who had no usage when the sweep came.
-- The log row goes with it: lifetime usage is used_traffic plus these rows, so keeping
-- it would count the traffic twice. A user this puts back over their limit is limited
-- again by the next review, which is where they were before the update.
WITH since AS (
    SELECT max(tstamp) AS at FROM goose_db_version WHERE version_id = 32 AND is_applied
), candidate AS (
    SELECT l.id, l.user_id, l.used_traffic_at_reset, l.reset_at,
        min(l.reset_at) OVER () AS sweep_start,
        count(*) OVER () AS swept
    FROM user_usage_logs l
    JOIN users u ON u.id = l.user_id
    CROSS JOIN since
    CROSS JOIN LATERAL (SELECT CASE u.data_limit_reset_strategy
        WHEN 'day' THEN interval '1 day'
        WHEN 'week' THEN interval '1 week'
        WHEN 'month' THEN interval '1 month'
        WHEN 'year' THEN interval '1 year'
    END AS length) period
    WHERE l.reset_at >= since.at
      AND u.last_usage_reset_at = l.reset_at
      AND u.created_at <= l.reset_at - period.length
      AND l.reset_at > now() - period.length
      AND NOT EXISTS (
          SELECT 1 FROM user_usage_logs other
          WHERE other.user_id = l.user_id AND other.id <> l.id AND other.reset_at >= since.at
      )
), wiped AS (
    SELECT id, user_id, used_traffic_at_reset FROM candidate
    WHERE reset_at < sweep_start + make_interval(mins => (swept / 500 + 5)::int)
), restored AS (
    UPDATE users u SET used_traffic = u.used_traffic + w.used_traffic_at_reset
    FROM wiped w WHERE u.id = w.user_id
)
DELETE FROM user_usage_logs WHERE id IN (SELECT id FROM wiped);

-- +goose Down
-- Nothing to undo: the update's own backup holds the values from before this ran.
