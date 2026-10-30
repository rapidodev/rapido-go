-- name: GetPendingResellerAPIUsage :many
-- Everything the reseller bot has not yet accepted, per admin, with the
-- username the bot knows the reseller by.
SELECT q.admin_id, a.username, q.pending
FROM reseller_api_usage_queue q
JOIN admins a ON a.id = q.admin_id
WHERE q.pending > 0
ORDER BY q.admin_id;

-- name: MarkResellerAPIUsageReported :exec
-- Subtracts exactly what was sent rather than zeroing the row: a node
-- report that lands while the POST is in flight adds to `pending`, and that
-- traffic must still go out with the next report.
UPDATE reseller_api_usage_queue q
SET pending = q.pending - v.sent, reported = q.reported + v.sent
FROM (SELECT unnest(sqlc.arg('admin_ids')::int[]) AS admin_id, unnest(sqlc.arg('sent')::bigint[]) AS sent) AS v
WHERE q.admin_id = v.admin_id;

-- name: GetActiveResellerBatch :one
SELECT * FROM reseller_delivery_batches WHERE active LIMIT 1;
-- name: CreateResellerBatch :one
INSERT INTO reseller_delivery_batches(id,destination,secret,payload,admin_ids,deltas) VALUES($1,$2,$3,$4,$5,$6) RETURNING *;
-- name: CompleteResellerBatch :exec
WITH sent AS (
 SELECT unnest(admin_ids) AS admin_id,unnest(deltas) AS amount FROM reseller_delivery_batches b WHERE b.id=$1 AND b.active
), locked AS (
 SELECT q.admin_id FROM reseller_api_usage_queue q WHERE q.admin_id IN (SELECT admin_id FROM sent) ORDER BY q.admin_id FOR NO KEY UPDATE
), billed AS (
 UPDATE reseller_api_usage_queue q SET pending=q.pending-sent.amount,reported=q.reported+sent.amount FROM sent JOIN locked ON locked.admin_id=sent.admin_id WHERE q.admin_id=sent.admin_id
)
UPDATE reseller_delivery_batches b SET active=false,secret='' WHERE b.id=$1 AND b.active;
