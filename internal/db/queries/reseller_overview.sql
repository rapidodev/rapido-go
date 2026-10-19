-- name: AdminOverviewCounts :many
-- One row per owning admin (a NULL admin_id row holds the users nobody owns,
-- for example those of the env-bootstrap sudo account): configs by status,
-- traffic, and how many are about to expire or run out of data. The window
-- edges are unix seconds computed by the caller. Only "active" configs count
-- as expiring or low on data - those are the ones a reseller can still act on;
-- limited and expired ones show in the status counts.
SELECT
    u.admin_id,
    count(*)::bigint AS total,
    count(*) FILTER (WHERE u.status = 'active')::bigint AS active,
    count(*) FILTER (WHERE u.status = 'disabled')::bigint AS disabled,
    count(*) FILTER (WHERE u.status = 'limited')::bigint AS limited,
    count(*) FILTER (WHERE u.status = 'expired')::bigint AS expired,
    count(*) FILTER (WHERE u.status = 'on_hold')::bigint AS on_hold,
    COALESCE(sum(u.used_traffic), 0)::bigint AS used_traffic,
    COALESCE(sum(u.data_limit), 0)::bigint AS data_limit,
    count(*) FILTER (WHERE u.data_limit IS NULL)::bigint AS unlimited,
    count(*) FILTER (WHERE u.status = 'active' AND u.expire IS NOT NULL AND u.expire > @now::int AND u.expire <= @in3::int)::bigint AS expiring_3d,
    count(*) FILTER (WHERE u.status = 'active' AND u.expire IS NOT NULL AND u.expire > @now::int AND u.expire <= @in7::int)::bigint AS expiring_7d,
    count(*) FILTER (WHERE u.status = 'active' AND u.data_limit IS NOT NULL AND u.data_limit > 0 AND (u.data_limit - u.used_traffic) * 10 <= u.data_limit)::bigint AS low_data
FROM users u
WHERE (sqlc.narg('admin_id')::int IS NULL OR u.admin_id = sqlc.narg('admin_id')::int)
GROUP BY u.admin_id;

-- name: AdminTopConsumers :many
-- The five configs that used the most traffic, per admin.
SELECT t.admin_id, t.username, t.used_traffic, t.data_limit
FROM (
    SELECT u.admin_id, u.username, u.used_traffic, u.data_limit,
           row_number() OVER (PARTITION BY u.admin_id ORDER BY u.used_traffic DESC, u.id) AS rn
    FROM users u
    WHERE (sqlc.narg('admin_id')::int IS NULL OR u.admin_id = sqlc.narg('admin_id')::int)
) t
WHERE t.rn <= 5
ORDER BY t.admin_id NULLS LAST, t.rn;

-- name: CountUsersByUsernamesPerAdmin :many
-- How many of the given (currently connected) usernames each admin owns.
SELECT admin_id, count(*)::bigint AS online
FROM users
WHERE username = ANY(@usernames::text[])
GROUP BY admin_id;

-- name: CountOnlineUsersPerAdminSince :many
-- The database definition of online, per admin, for when live presence is not
-- available.
SELECT admin_id, count(*)::bigint AS online
FROM users
WHERE online_at >= @cutoff::timestamptz
GROUP BY admin_id;

-- name: ListAdminsWithTelegram :many
-- Admins a daily digest can be sent to.
SELECT * FROM admins WHERE telegram_id IS NOT NULL ORDER BY id;

-- name: ListUsersByUsernames :many
-- The users among the given names, for a bulk action.
SELECT * FROM users WHERE username = ANY(@usernames::text[]);
