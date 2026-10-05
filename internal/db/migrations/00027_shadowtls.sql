-- +goose Up
-- ShadowTLS (sing-box type "shadowtls") as a real inbound protocol - see
-- internal/nodecore/shadowtls's own doc comment for the fork. v3-only
-- (v1/v2 have no real per-user credential at all). Its "handshake server"
-- setting is hardcoded to wildcard_sni=all (dial whatever SNI the
-- client's own ClientHello claims) rather than an admin-configured fixed
-- fallback target, and security is "none" at this table's own
-- security/reality_*/tls_* columns (ShadowTLS relays a real TLS
-- handshake rather than terminating one itself - the TLS block a client
-- needs instead describes that dial, which these columns have no row
-- for; see the fork's own doc comment).
--
-- Unlike Naive, this protocol DOES need new inbound-level columns: a bare
-- ShadowTLS inbound has no concept of "where to send the client's actual
-- traffic" at all (it is a disguise/auth layer, not a complete proxy
-- protocol on its own) - this fork pairs it with an embedded
-- shadowsocks-2022 inner data layer, sharing ONE method+key across every
-- user of the inbound (the outer ShadowTLS password, already in
-- proxies.settings jsonb, already distinguishes users; the inner layer
-- only needs to decrypt), the same inbound-level-secret-plus-per-user-
-- credential split snell_psk/snell_v6_mode already established for Snell
-- in 00019.
ALTER TABLE inbounds DROP CONSTRAINT inbounds_protocol_check;
ALTER TABLE inbounds ADD CONSTRAINT inbounds_protocol_check
    CHECK (protocol IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria', 'naive', 'shadowtls'));

ALTER TABLE proxies DROP CONSTRAINT proxies_type_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_type_check
    CHECK (type IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria', 'naive', 'shadowtls'));

-- shadowtls_inner_method is one of the three shadowsocks-2022 AEAD
-- methods (shadowaead_2022.List) - enforced in Go (internal/httpapi/
-- inbounds.go), matching this table's existing convention of leaving
-- length/shape checks to the application layer.
ALTER TABLE inbounds ADD COLUMN shadowtls_inner_method TEXT;
-- shadowtls_inner_password is the inner layer's base64 pre-shared key.
ALTER TABLE inbounds ADD COLUMN shadowtls_inner_password TEXT;

-- +goose Down
ALTER TABLE inbounds DROP COLUMN shadowtls_inner_password;
ALTER TABLE inbounds DROP COLUMN shadowtls_inner_method;

ALTER TABLE proxies DROP CONSTRAINT proxies_type_check;
ALTER TABLE proxies ADD CONSTRAINT proxies_type_check
    CHECK (type IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria', 'naive'));

ALTER TABLE inbounds DROP CONSTRAINT inbounds_protocol_check;
ALTER TABLE inbounds ADD CONSTRAINT inbounds_protocol_check
    CHECK (protocol IN ('vmess', 'vless', 'trojan', 'shadowsocks', 'hysteria2', 'tuic', 'snell', 'anytls', 'hysteria', 'naive'));
