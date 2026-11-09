-- +goose Up
-- Whether subscriptions tell client apps to send Iranian sites and IPs
-- straight out of the user's own connection instead of through the VPN.
-- NULL means "use SUBSCRIPTION_IRAN_DIRECT from the environment", which
-- defaults to on - the same override rule as every other column here.
ALTER TABLE integration_settings ADD COLUMN subscription_iran_direct BOOLEAN;

-- +goose Down
ALTER TABLE integration_settings DROP COLUMN subscription_iran_direct;
