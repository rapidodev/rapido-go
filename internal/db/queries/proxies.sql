-- name: CreateProxy :one
INSERT INTO proxies (user_id, type, settings) VALUES ($1, $2, $3)
RETURNING *;

-- name: ListProxiesByUserID :many
SELECT * FROM proxies WHERE user_id = $1 ORDER BY id;

-- name: ListProxiesByUserIDs :many
-- Batched form of ListProxiesByUserID for GET /api/users - the caller
-- groups rows by user_id in Go, avoiding one query per row on a page.
SELECT * FROM proxies WHERE user_id = ANY($1::int[]) ORDER BY user_id, id;

-- name: DeleteProxyByID :exec
DELETE FROM proxies WHERE id = $1;

-- name: DeleteProxiesByUserID :exec
-- The composite PK added to exclude_inbounds_association (00001_init_schema.sql)
-- removes the StaleDataError risk that made the current Python code delete
-- and recreate proxies through raw Core statements instead of a normal
-- ORM cascade - a plain delete+recreate on update is safe here.
DELETE FROM proxies WHERE user_id = $1;

-- name: ReplaceExcludedInbounds :exec
INSERT INTO exclude_inbounds_association (proxy_id, inbound_tag)
SELECT $1, unnest($2::text[]);

-- name: DeleteExcludedInbounds :exec
DELETE FROM exclude_inbounds_association WHERE proxy_id = $1;

-- name: ListExcludedInboundTags :many
SELECT inbound_tag FROM exclude_inbounds_association WHERE proxy_id = $1 ORDER BY inbound_tag;

-- name: ListExcludedInboundTagsByProxyIDs :many
-- Batched form of ListExcludedInboundTags - unlike that one, this returns
-- proxy_id alongside each tag so the caller can group rows in Go.
SELECT proxy_id, inbound_tag FROM exclude_inbounds_association
WHERE proxy_id = ANY($1::int[]) ORDER BY proxy_id, inbound_tag;

-- name: ProxyCredentialInUse :one
-- Whether another user holds this credential, whatever that user's status: a
-- disabled account can be enabled again. Asked only about a credential a client
-- supplied, so the scan is paid for rarely. A UUID is stored lowercase since
-- writes canonicalize it, but rows from before that may not be.
SELECT EXISTS (
    SELECT 1 FROM proxies
    WHERE type = sqlc.arg(type)
      AND user_id IS DISTINCT FROM sqlc.arg(user_id)::int
      AND (settings ->> sqlc.arg(field)::text = sqlc.arg(value)::text
           OR (sqlc.arg(field)::text = 'id' AND lower(settings ->> sqlc.arg(field)::text) = sqlc.arg(value)::text))
);

-- name: UpdateProxySettings :one
UPDATE proxies SET settings = $2 WHERE id = $1
RETURNING *;

-- name: CountProxiesOutsideProtocols :many
-- How many users would lose a credential if only these protocols had inbounds.
SELECT type, count(*) AS users FROM proxies
WHERE type <> ALL(sqlc.arg(protocols)::text[])
GROUP BY type ORDER BY type;

-- name: PruneOrphanedProxies :many
-- Deletes every proxy whose protocol has no inbound left at all - called
-- after any change that could remove the last inbound of a protocol
-- (deleting one inbound, or the Xray-config JSON editor's full-replace
-- Apply), so a protocol dropped from the live config doesn't leave dead
-- credentials sitting in every affected user's proxies forever. A user
-- gaining or losing a proxy row this way is exactly the same shape as any
-- other proxy change (subscription links/configs are always generated
-- from whatever proxies currently exist), so no extra invalidation beyond
-- what the caller already does for the inbound change itself is needed.
DELETE FROM proxies
WHERE type NOT IN (SELECT DISTINCT protocol FROM inbounds)
RETURNING *;
