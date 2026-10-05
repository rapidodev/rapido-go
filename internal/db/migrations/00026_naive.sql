-- +goose Up
-- Naive (sing-box type "naive") as a real inbound protocol - see
-- internal/nodecore/naive's own doc comment for the fork (TCP-only; UDP/
-- HTTP3 not supported yet). Needs no new inbound-level column at all,
-- unlike every other protocol-support migration before it: TLS is optional
-- rather than mandatory (naive can run over plain HTTP, though almost
-- nobody would in practice), and its only per-user credential is a
-- username+password pair that fits entirely in the existing proxies.settings
-- jsonb - the username is simply the account's own name, not a separate
-- secret (see internal/proxysettings.NaiveSettings's own doc comment).
ALTER TABLE inbounds DROP CONSTRAINT inbounds_protocol_check;
ALTER TABLE inbounds ADD CONSTRAINT inbounds_protocol_check
    CHECK (protocol IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria', 'naive'));

ALTER TABLE proxies DROP CONSTRAINT proxies_type_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_type_check
    CHECK (type IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria', 'naive'));

-- +goose Down
ALTER TABLE proxies DROP CONSTRAINT proxies_type_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_type_check
    CHECK (type IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria'));

ALTER TABLE inbounds DROP CONSTRAINT inbounds_protocol_check;
ALTER TABLE inbounds ADD CONSTRAINT inbounds_protocol_check
    CHECK (protocol IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria'));
