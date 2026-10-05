-- +goose Up
-- Hysteria v1 (sing-box type "hysteria", not to be confused with the
-- already-supported "hysteria2") as a real inbound protocol - see
-- internal/nodecore/hysteria's own doc comment for the fork. Reuses the
-- up_mbps/down_mbps columns 00018 already added for hysteria2's inbound,
-- but with the opposite nullability rule: hysteria2 defaults to BBR and
-- only takes these as an optional hint, while Hysteria v1 has no such
-- congestion-control fallback and genuinely needs a declared bandwidth
-- ceiling to function at all (the same asymmetry Core Config's own
-- outbound side already enforces for these two protocols) - enforced at
-- the application layer in internal/httpapi/inbounds.go, not here, same as
-- every other protocol-specific "required when protocol=X" rule in this
-- table.
ALTER TABLE inbounds DROP CONSTRAINT inbounds_protocol_check;
ALTER TABLE inbounds ADD CONSTRAINT inbounds_protocol_check
    CHECK (protocol IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria'));

ALTER TABLE proxies DROP CONSTRAINT proxies_type_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_type_check
    CHECK (type IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria'));

-- Hysteria v1's own obfuscation (XPlus - a single shared password, simpler
-- than hysteria2's salamander/gecko) - optional, named distinctly from
-- hysteria2_obfs_password since the two are different obfuscation schemes
-- entirely, not interchangeable values of one setting.
ALTER TABLE inbounds ADD COLUMN hysteria_obfs_password TEXT;

-- +goose Down
ALTER TABLE inbounds DROP COLUMN hysteria_obfs_password;

ALTER TABLE proxies DROP CONSTRAINT proxies_type_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_type_check
    CHECK (type IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls'));

ALTER TABLE inbounds DROP CONSTRAINT inbounds_protocol_check;
ALTER TABLE inbounds ADD CONSTRAINT inbounds_protocol_check
    CHECK (protocol IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls'));
