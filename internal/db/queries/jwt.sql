-- name: GetJWTSecret :one
SELECT * FROM jwt_secrets ORDER BY id LIMIT 1;

-- name: CreateJWTSecret :one
INSERT INTO jwt_secrets (id,secret_key) VALUES (1,$1)
ON CONFLICT(id) DO UPDATE SET secret_key=jwt_secrets.secret_key
RETURNING *;

-- name: ReplaceJWTSecret :exec
-- Used only by a legacy-panel import, to adopt the SOURCE panel's signing
-- key. Without it every subscription link already installed in a customer's
-- client app stops validating the moment the migration completes.
INSERT INTO jwt_secrets (id, secret_key) VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET secret_key = EXCLUDED.secret_key;

-- name: EnsureAdminTokenKey :one
-- Whichever process gets here first decides the key, and every other one reads it.
UPDATE jwt_secrets SET admin_token_key = COALESCE(admin_token_key, $1), admin_token_key_since = COALESCE(admin_token_key_since, now())
WHERE id = (SELECT id FROM jwt_secrets ORDER BY id LIMIT 1)
RETURNING admin_token_key, admin_token_key_since;
