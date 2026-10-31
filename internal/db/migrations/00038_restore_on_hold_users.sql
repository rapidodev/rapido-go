-- +goose Up
-- Disabling and then enabling a user who was waiting for their first connection left
-- them active with no expiry, so a plan of 30 days from first use never ended. Their
-- rows still have the hold fields, which starting a plan properly clears. They go back
-- to waiting and the plan starts when they next connect: the whole plan again, rather
-- than cutting anybody off over time nobody was counting.
UPDATE users SET status = 'on_hold', last_status_change = now()
WHERE status = 'active' AND COALESCE(expire, 0) = 0 AND on_hold_expire_duration > 0;

-- The same users if they have since used up their traffic. They have started, so the
-- plan runs from now, or a renewal or a period reset would bring them back with no
-- expiry all over again.
UPDATE users SET
    expire = LEAST(extract(epoch FROM now())::bigint + on_hold_expire_duration, 2147483647)::int,
    on_hold_expire_duration = NULL,
    on_hold_timeout = NULL
WHERE status = 'limited' AND COALESCE(expire, 0) = 0 AND on_hold_expire_duration > 0;

-- +goose Down
-- Nothing to undo: the update's own backup holds the values from before this ran.
