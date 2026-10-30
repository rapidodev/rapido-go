-- name: GetUsersNeedingStatusReview :many
-- Only users this job can actually act on - a faithful SQL translation of
-- review_users.py's `limited`/`expired` checks (zero/NULL semantics
-- included: a data_limit or expire of 0/NULL never triggers either
-- condition), pushed down to avoid loading every active user on every
-- tick. See that file's own comment: unfiltered, this was the slowest
-- single query in the whole system in production.
SELECT * FROM users
WHERE status = 'active'
  AND (
    (data_limit IS NOT NULL AND data_limit > 0 AND used_traffic >= data_limit)
    OR (expire IS NOT NULL AND expire != 0 AND expire <= $1)
  );

-- name: GetOnHoldUsers :many
SELECT * FROM users WHERE status = 'on_hold';

-- name: ApplyReviewedStatus :execrows
-- Limits or expires a user the job read a moment ago, if that still holds: a
-- renewal in between would otherwise leave them limited with no usage, and
-- nothing ever flips that back.
UPDATE users SET status = sqlc.arg(status)::text, last_status_change = now()
WHERE id = sqlc.arg(id) AND status = 'active' AND CASE sqlc.arg(status)::text
    WHEN 'limited' THEN data_limit > 0 AND used_traffic >= data_limit
    WHEN 'expired' THEN expire IS NOT NULL AND expire <> 0 AND expire <= extract(epoch FROM now())
    ELSE false
END;

-- name: ActivateOnHoldUser :execrows
-- Combines crud.update_user_status(active) + crud.start_user_expire into
-- one statement - both mutate the same row with no observable
-- intermediate state, so there's no reason to round-trip twice. Sets
-- expire = now + on_hold_expire_duration and clears both hold fields,
-- exactly like start_user_expire.
UPDATE users SET
    status = 'active',
    last_status_change = now(),
    expire = extract(epoch FROM now())::int + on_hold_expire_duration,
    on_hold_expire_duration = NULL,
    on_hold_timeout = NULL
WHERE id = $1 AND status = 'on_hold';

-- name: ClearNodeUserUsages :exec
-- Mirrors crud._clear_node_usages - called by both the manual reset-usage
-- endpoint and NextPlan firing.
DELETE FROM node_user_usages WHERE user_id = $1;

-- name: ResetUserByNextPlan :one
-- Mirrors crud.reset_user_by_next's formula exactly, computed here in one
-- statement rather than read-then-write in Go: new data_limit = plan's
-- data_limit, plus the unused remainder of the old limit UNLESS
-- add_remaining_traffic discards it (see the Go rewrite's memory note
-- flagging this field's name as likely inverted from its actual effect -
-- ported as-is, not fixed, since nothing here changes that behavior).
--
-- A plan that names no limit or no expiry leaves that one as it is. What's left
-- of the old limit is never below zero and an unlimited user has none, so a user
-- past their limit doesn't start the new plan in debt, or with no limit at all.
UPDATE users u SET
    status = 'active',
    last_status_change = now(),
    data_limit = CASE WHEN np.data_limit > 0
        THEN np.data_limit + (CASE WHEN np.add_remaining_traffic THEN 0 ELSE GREATEST(COALESCE(u.data_limit, 0) - u.used_traffic, 0) END)
        ELSE u.data_limit END,
    expire = CASE WHEN COALESCE(np.expire, 0) > 0 THEN extract(epoch FROM now())::int + np.expire ELSE u.expire END,
    used_traffic = 0
FROM next_plans np
WHERE u.id = $1 AND np.user_id = u.id
RETURNING u.*;

-- name: ResetDuePeriodicUsers :many
WITH due AS (
 SELECT * FROM users WHERE data_limit_reset_strategy != 'no_reset'
 AND COALESCE(last_usage_reset_at,created_at) <= now() - CASE data_limit_reset_strategy
 WHEN 'day' THEN interval '1 day' WHEN 'week' THEN interval '1 week'
 WHEN 'month' THEN interval '1 month' WHEN 'year' THEN interval '1 year' ELSE interval '1000 years' END
 ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 500
), logged AS (
 INSERT INTO user_usage_logs(user_id,used_traffic_at_reset) SELECT id,used_traffic FROM due WHERE used_traffic>0
)
UPDATE users u SET used_traffic=0,last_usage_reset_at=now(),
 status=CASE WHEN u.status='limited' AND (u.expire IS NULL OR u.expire=0 OR u.expire>extract(epoch FROM now())) THEN 'active' ELSE u.status END
FROM due WHERE u.id=due.id RETURNING u.*;

-- name: RemoveAutoExpiredUsers :many
WITH due AS (
 SELECT id FROM users WHERE status IN ('expired','limited') AND auto_delete_in_days>0
 AND last_status_change<=now()-make_interval(days=>auto_delete_in_days)
 ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 500
)
DELETE FROM users WHERE id IN(SELECT id FROM due) RETURNING *;
