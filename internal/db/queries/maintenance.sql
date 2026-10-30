-- name: PruneNodeReportReceipts :execrows
-- A receipt only matters while its report could still be retried, and a node's
-- spool doesn't sit on one for a month.
DELETE FROM node_report_receipts WHERE received_at < now() - interval '30 days';

-- name: PruneCoreConfigSnapshots :execrows
-- History stays useful for what's recent, and whatever its age the newest few
-- are all there is to go back to.
DELETE FROM core_config_snapshots
WHERE created_at < now() - interval '90 days'
  AND id NOT IN (SELECT id FROM core_config_snapshots ORDER BY id DESC LIMIT 200);

-- name: PruneResellerBatches :execrows
DELETE FROM reseller_delivery_batches WHERE NOT active AND created_at < now() - interval '30 days';

-- name: PruneStaleNotifications :execrows
-- A notification nobody could deliver for a week is no longer news. Gateway
-- deliveries are left alone: they are changes a replica still has to learn.
DELETE FROM delivery_outbox WHERE kind = 'notification' AND created_at < now() - interval '7 days';

-- name: FailInterruptedTunnelProvisioning :execrows
-- Provisioning runs in the API process with a three minute limit: one still
-- pending long after that was cut off by a restart, and nothing else will ever
-- move it on.
UPDATE tunnels SET status = 'failed', status_message = 'Provisioning was interrupted by a panel restart; delete this tunnel and create it again.'
WHERE status = 'pending' AND created_at < now() - interval '30 minutes';
