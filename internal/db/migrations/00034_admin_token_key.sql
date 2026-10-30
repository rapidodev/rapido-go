-- +goose Up
-- Admin tokens get a key of their own. secret_key signs subscription links, and
-- it's imported along with a legacy panel's data so old links keep working: sharing
-- it would let anyone holding that old panel's database sign themselves in as any
-- admin here. NULL until the panel next starts or reads it (EnsureAdminTokenKey).
-- admin_token_key_since is when it took over: tokens already out there keep working
-- until they would have expired anyway, so nobody is logged out and no bot holding
-- one breaks.
ALTER TABLE jwt_secrets ADD COLUMN admin_token_key TEXT;
ALTER TABLE jwt_secrets ADD COLUMN admin_token_key_since TIMESTAMPTZ;

-- +goose Down
ALTER TABLE jwt_secrets DROP COLUMN admin_token_key_since;
ALTER TABLE jwt_secrets DROP COLUMN admin_token_key;
