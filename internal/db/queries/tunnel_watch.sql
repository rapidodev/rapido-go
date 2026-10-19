-- name: InsertTunnelSample :exec
INSERT INTO tunnel_samples (
    tunnel_id, at,
    relay_rx_bps, relay_tx_bps, node_rx_bps, node_tx_bps,
    connections, cpu,
    v6_rtt_ms, v6_loss, v6_jitter_ms, v4_rtt_ms, v4_loss, v4_jitter_ms,
    ha_active, ha_primary_up, ha_backup_up
) VALUES (
    $1, $2,
    $3, $4, $5, $6,
    $7, $8,
    $9, $10, $11, $12, $13, $14,
    $15, $16, $17
)
ON CONFLICT (tunnel_id, at) DO NOTHING;

-- name: PruneTunnelSamples :execrows
DELETE FROM tunnel_samples WHERE at < $1;

-- name: PruneTunnelEvents :execrows
DELETE FROM tunnel_events WHERE at < $1;

-- name: InsertTunnelEvent :one
INSERT INTO tunnel_events (tunnel_id, at, kind, severity, detail, duration_seconds)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: ListTunnelEvents :many
-- Newest first. kind NULL means every kind.
SELECT * FROM tunnel_events
WHERE tunnel_id = @tunnel_id
  AND at >= @since
  AND (sqlc.narg('kind')::text IS NULL OR kind = sqlc.narg('kind')::text)
ORDER BY at DESC, id DESC
LIMIT @row_limit;

-- name: ListTunnelEventsTimeline :many
-- Events of every tunnel inside the window, newest first, with the tunnel's
-- name so a combined timeline needs no second lookup.
SELECT e.id, e.tunnel_id, t.name AS tunnel_name, e.at, e.kind, e.severity, e.detail, e.duration_seconds
FROM tunnel_events e
JOIN tunnels t ON t.id = e.tunnel_id
WHERE e.at >= @since
ORDER BY e.at DESC, e.id DESC
LIMIT @row_limit;

-- name: LastTunnelEventTimes :many
-- When each kind of event last happened for a tunnel (inside the last day) -
-- seeds the in-memory rate limiter after a restart so a restart never repeats
-- an alert that was just sent.
SELECT kind, max(at)::timestamptz AS last_at
FROM tunnel_events
WHERE tunnel_id = $1 AND at >= $2
GROUP BY kind;

-- name: GetTunnelByRelayID :one
SELECT * FROM tunnels WHERE tunnel_relay_id = $1 LIMIT 1;

-- name: ListTunnelSamplesBinned :many
-- Samples of one tunnel since a moment, averaged into buckets of
-- bucket_secs seconds (date_bin), so a chart never receives more than a few
-- hundred points however long the window is. The *_max columns keep the
-- peak of each bucket: an average hides exactly the flat-topped plateau the
-- dashboard wants to show. ha_primary_up/ha_backup_up are 1 only when they
-- were true in every sample of the bucket (0 when any sample had them false),
-- so a dip is not averaged away either.
--
-- A column with no measurement in the bucket is -1 (ha_active: empty string),
-- never NULL: sqlc types aggregates as non-null, and a real measurement is
-- never negative. The handler turns -1 back into JSON null.
SELECT
    date_bin(make_interval(secs => @bucket_secs::float8), at, TIMESTAMPTZ '2001-01-01 00:00:00+00')::timestamptz AS bucket,
    COALESCE(avg(relay_rx_bps), -1)::float8 AS relay_rx_bps,
    COALESCE(avg(relay_tx_bps), -1)::float8 AS relay_tx_bps,
    COALESCE(avg(node_rx_bps), -1)::float8 AS node_rx_bps,
    COALESCE(avg(node_tx_bps), -1)::float8 AS node_tx_bps,
    COALESCE(max(relay_rx_bps), -1)::float8 AS relay_rx_bps_max,
    COALESCE(max(relay_tx_bps), -1)::float8 AS relay_tx_bps_max,
    COALESCE(max(node_rx_bps), -1)::float8 AS node_rx_bps_max,
    COALESCE(max(node_tx_bps), -1)::float8 AS node_tx_bps_max,
    COALESCE(avg(connections), -1)::float8 AS connections,
    COALESCE(avg(cpu), -1)::float8 AS cpu,
    COALESCE(avg(v6_rtt_ms), -1)::float8 AS v6_rtt_ms,
    COALESCE(avg(v6_loss), -1)::float8 AS v6_loss,
    COALESCE(max(v6_loss), -1)::float8 AS v6_loss_max,
    COALESCE(avg(v6_jitter_ms), -1)::float8 AS v6_jitter_ms,
    COALESCE(avg(v4_rtt_ms), -1)::float8 AS v4_rtt_ms,
    COALESCE(avg(v4_loss), -1)::float8 AS v4_loss,
    COALESCE(max(v4_loss), -1)::float8 AS v4_loss_max,
    COALESCE(avg(v4_jitter_ms), -1)::float8 AS v4_jitter_ms,
    COALESCE((array_agg(ha_active ORDER BY at DESC) FILTER (WHERE ha_active IS NOT NULL))[1], '')::text AS ha_active,
    (CASE WHEN count(ha_primary_up) = 0 THEN -1 WHEN bool_and(ha_primary_up) THEN 1 ELSE 0 END)::int AS ha_primary_up,
    (CASE WHEN count(ha_backup_up) = 0 THEN -1 WHEN bool_and(ha_backup_up) THEN 1 ELSE 0 END)::int AS ha_backup_up,
    count(*)::int AS samples
FROM tunnel_samples
WHERE tunnel_id = @tunnel_id AND at >= @since
GROUP BY 1
ORDER BY 1;

-- name: ListTunnelSamplesRaw :many
-- Unaggregated samples for the Network Doctor's statistics.
SELECT * FROM tunnel_samples
WHERE tunnel_id = $1 AND at >= $2
ORDER BY at;
