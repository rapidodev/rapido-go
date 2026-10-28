-- name: SaveCoreSnapshot :exec
INSERT INTO core_config_snapshots(config,reason) VALUES($1,$2);
-- name: ListCoreSnapshots :many
SELECT id,created_at,reason FROM core_config_snapshots ORDER BY id DESC LIMIT 100;
-- name: GetCoreSnapshot :one
SELECT * FROM core_config_snapshots WHERE id=$1;
-- name: EnqueueDelivery :exec
INSERT INTO delivery_outbox(kind,destination,payload) VALUES($1,$2,$3);

-- name: CaptureFleetConfig :one
SELECT jsonb_build_object(
 'snapshot_version',1,
 'core',(SELECT to_jsonb(c) FROM core_config c LIMIT 1),
 'inbounds',COALESCE((SELECT jsonb_agg(to_jsonb(i) ORDER BY tag) FROM inbounds i),'[]'::jsonb),
 'hosts',COALESCE((SELECT jsonb_agg(to_jsonb(h) ORDER BY priority,id) FROM hosts h),'[]'::jsonb),
 'exclusions',COALESCE((SELECT jsonb_agg(to_jsonb(e)) FROM exclude_inbounds_association e),'[]'::jsonb),
 'templates',COALESCE((SELECT jsonb_agg(to_jsonb(t)) FROM template_inbounds_association t),'[]'::jsonb),
 'nodes',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'inbound_tags',inbound_tags,'listen_ports',listen_ports,'core_overrides',core_overrides)) FROM nodes),'[]'::jsonb)
)::jsonb AS config;

-- name: RestoreFleetInbounds :exec
INSERT INTO inbounds(tag,protocol,network,header_type,security,reality_private_key,reality_short_ids,reality_server_name,reality_server_port,tls_certificate,tls_key,tls_server_name,hysteria2_obfs_password,up_mbps,down_mbps,congestion_control,zero_rtt_handshake,snell_psk,snell_v6_mode,hysteria_obfs_password,shadowtls_inner_method,shadowtls_inner_password) SELECT tag,protocol,network,header_type,security,reality_private_key,reality_short_ids,reality_server_name,reality_server_port,tls_certificate,tls_key,tls_server_name,hysteria2_obfs_password,up_mbps,down_mbps,congestion_control,zero_rtt_handshake,snell_psk,snell_v6_mode,hysteria_obfs_password,shadowtls_inner_method,shadowtls_inner_password FROM jsonb_populate_recordset(NULL::inbounds,sqlc.arg('config')::jsonb->'inbounds') ON CONFLICT(tag) DO UPDATE SET protocol=EXCLUDED.protocol,network=EXCLUDED.network,header_type=EXCLUDED.header_type,security=EXCLUDED.security,reality_private_key=EXCLUDED.reality_private_key,reality_short_ids=EXCLUDED.reality_short_ids,reality_server_name=EXCLUDED.reality_server_name,reality_server_port=EXCLUDED.reality_server_port,tls_certificate=EXCLUDED.tls_certificate,tls_key=EXCLUDED.tls_key,tls_server_name=EXCLUDED.tls_server_name,hysteria2_obfs_password=EXCLUDED.hysteria2_obfs_password,up_mbps=EXCLUDED.up_mbps,down_mbps=EXCLUDED.down_mbps,congestion_control=EXCLUDED.congestion_control,zero_rtt_handshake=EXCLUDED.zero_rtt_handshake,snell_psk=EXCLUDED.snell_psk,snell_v6_mode=EXCLUDED.snell_v6_mode,hysteria_obfs_password=EXCLUDED.hysteria_obfs_password,shadowtls_inner_method=EXCLUDED.shadowtls_inner_method,shadowtls_inner_password=EXCLUDED.shadowtls_inner_password;

-- name: DeleteNonSnapshotInbounds :exec
DELETE FROM inbounds WHERE tag NOT IN (SELECT value->>'tag' FROM jsonb_array_elements(sqlc.arg('config')::jsonb->'inbounds'));
-- name: DeleteFleetHosts :exec
DELETE FROM hosts;
-- name: RestoreFleetHosts :exec
INSERT INTO hosts(remark,address,port,path,sni,host,security,alpn,fingerprint,inbound_tag,allowinsecure,is_disabled,mux_enable,fragment_setting,noise_setting,random_user_agent,use_sni_as_host,priority) SELECT remark,address,port,path,sni,host,security,alpn,fingerprint,inbound_tag,allowinsecure,is_disabled,mux_enable,fragment_setting,noise_setting,random_user_agent,use_sni_as_host,priority FROM jsonb_populate_recordset(NULL::hosts,sqlc.arg('config')::jsonb->'hosts');

-- name: ClearFleetExclusions :exec
DELETE FROM exclude_inbounds_association;
-- name: RestoreFleetExclusions :exec
INSERT INTO exclude_inbounds_association(proxy_id,inbound_tag)
SELECT e.proxy_id,e.inbound_tag FROM jsonb_populate_recordset(NULL::exclude_inbounds_association,sqlc.arg('config')::jsonb->'exclusions') e JOIN proxies p ON p.id=e.proxy_id JOIN inbounds i ON i.tag=e.inbound_tag;
-- name: ClearFleetTemplateInbounds :exec
DELETE FROM template_inbounds_association;
-- name: RestoreFleetTemplateInbounds :exec
INSERT INTO template_inbounds_association(user_template_id,inbound_tag)
SELECT e.user_template_id,e.inbound_tag FROM jsonb_populate_recordset(NULL::template_inbounds_association,sqlc.arg('config')::jsonb->'templates') e JOIN user_templates t ON t.id=e.user_template_id JOIN inbounds i ON i.tag=e.inbound_tag;
-- name: RestoreFleetNodeProfiles :exec
UPDATE nodes n SET inbound_tags=s.inbound_tags,listen_ports=s.listen_ports,core_overrides=s.core_overrides
FROM jsonb_populate_recordset(NULL::nodes,sqlc.arg('config')::jsonb->'nodes') s WHERE n.id=s.id;
