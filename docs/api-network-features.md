# Network, reseller and node tools - API contract

New and changed endpoints, for the dashboard. Every example below is checked by
`internal/httpapi/docs_test.go`: it decodes each JSON block into the real Go
struct that produces the response (unknown field names fail the test), so the
shapes cannot drift from the code.

Conventions

- All paths are under `/api`. Authentication is the same bearer token as the rest of the API. **sudo** = sudo admin
  only; **admin** = any logged-in admin (a reseller is scoped to their own users).
- Errors are `{"detail": "..."}` with the status given per endpoint (some add fields such as `code`).
- Times are RFC 3339 UTC. Traffic rates are **bits per second**, byte counters are bytes, delays are milliseconds,
  loss is a percentage (0-100).
- Nothing in this document changes an existing field; new fields are additive.

Contents

1. History of a tunnel: samples, events, timeline (sudo)
2. Network Doctor (sudo)
3. Reseller dashboard and user tools (admin)
4. Node restart and `ssh_known` (sudo)
5. Config-sharing detection: not built
6. Settings and background jobs

---

## 1. History of a tunnel

The backend measures every active tunnel once a minute (`internal/tunnelmetrics`) and `internal/tunnelwatch` stores one
**sample** per tunnel per round (kept 14 days) and derives **events** (kept 90 days). Nothing is stored for a tunnel
while the panel's backend singleton is not running.

### New live field in `GET /api/tunnels`

`metrics.node.path` is the quality of the path from the node to the relay, from a burst of 10 pings 0.2 s apart over each
address family the relay has (IPv6 and IPv4 addresses come from `details.relay_addrs`, else `relay_tunnel_addr` /
`relay_host`). A family with no address is absent; a family that was tried and did not answer has `received: 0`,
`loss_pct: 100`.

<!-- example:node_path -->
```json
{
  "nic": "eth0",
  "rx_bps": 46292408,
  "tx_bps": 46582808,
  "rx_bytes": 2789604302821,
  "tx_bytes": 2799211108039,
  "path": {
    "v6": {
      "target": "2001:db8:3::fe4",
      "sent": 10,
      "received": 10,
      "loss_pct": 0,
      "min_ms": 89.936,
      "avg_ms": 90.136,
      "max_ms": 90.453,
      "jitter_ms": 0.172
    },
    "v4": {
      "target": "203.0.113.82",
      "sent": 10,
      "received": 9,
      "loss_pct": 10,
      "min_ms": 80.996,
      "avg_ms": 81.026,
      "max_ms": 81.101,
      "jitter_ms": 0.034
    }
  }
}
```

### Event kinds

| kind | severity | meaning | alerted |
| --- | --- | --- | --- |
| `tunnel_down` | critical | the relay stopped answering the health probe | by the existing relay alert |
| `tunnel_up` | info | it answers again; `duration_seconds` = how long it was down | by the existing relay alert |
| `failover` | warning | HAProxy moved the traffic from the primary to the backup relay path (also a primary that dropped and came back between two rounds, detected from the server's status age) | Telegram + Discord, at most one per 5 min |
| `failback` | info | traffic is on the primary again | same |
| `loss_high` | warning | at least 5% loss in 2 consecutive rounds on one address family | at most one per 30 min |
| `ceiling` | warning | traffic pinned flat: peak at least 100 Mbit/s and at least 60% of the last 20 samples within 5% of it | at most one per 2 h |
| `nic_saturated` | warning | a network card with a known link speed above 90% in 2 consecutive rounds | at most one per hour |
| `node_restart`, `node_reboot` | warning | a restart requested from the panel (section 5) | no |

Events of the same kind are also rate-limited when *recorded* (loss 15 min, ceiling and nic_saturated 30 min; failover and
failback are always recorded). The first round after the backend starts never raises a failover/failback, and the rate
limits are restored from the stored events, so a restart does not repeat an alert.

### `GET /api/tunnels/:id/events` (sudo)

Query: `limit` (1-1000, default 100), `since` (RFC 3339 or unix seconds, default 30 days back), `kind` (one kind).
Newest first. 404 for an unknown tunnel, 422 for a bad `since`.

<!-- example:events -->
```json
{
  "events": [
    {
      "id": 41,
      "tunnel_id": 3,
      "at": "2026-10-19T11:42:10Z",
      "kind": "failover",
      "severity": "warning",
      "detail": "Traffic moved from the primary relay path to the backup (relay_v4 203.0.113.82:7000); the primary server is relay_v6 DOWN.",
      "duration_seconds": null
    },
    {
      "id": 40,
      "tunnel_id": 3,
      "at": "2026-10-19T10:42:10Z",
      "kind": "tunnel_up",
      "severity": "info",
      "detail": "The relay answers again after 2m0s down.",
      "duration_seconds": 120
    }
  ],
  "tunnel_id": 3
}
```

### `GET /api/tunnels/timeline` (sudo)

Events of every tunnel. Query: `hours` (1-720, default 24), `limit` (1-2000, default 500). Newest first; each event has
`tunnel_name`.

<!-- example:timeline -->
```json
{
  "events": [
    {
      "id": 41,
      "tunnel_id": 3,
      "tunnel_name": "node2",
      "at": "2026-10-19T11:42:10Z",
      "kind": "loss_high",
      "severity": "warning",
      "detail": "IPv6 path from the node to the relay lost 10% of its probes (9 of 10 answered) in each of the last 2 checks; average round trip 90.2 ms.",
      "duration_seconds": null
    }
  ],
  "hours": 24
}
```

### `GET /api/tunnels/:id/samples` (sudo)

Query: `hours` (1-336, default 24). The samples are averaged into buckets of `bucket_seconds` (at least 60, never more than
300 points). The `*_max` fields keep each bucket's peak (an average hides a flat top or a short loss burst). Everything
measured is `null` when nothing was measured in the bucket (the box was unreachable, no address of that family, no
HAProxy). `ha_primary_up` / `ha_backup_up` are true only if they were true in every sample of the bucket. `events` holds
the tunnel's events of the same window (at most 200) so a chart can mark them.

<!-- example:samples -->
```json
{
  "bucket_seconds": 300,
  "events": [],
  "hours": 24,
  "points": [
    {
      "t": "2026-10-19T11:37:10Z",
      "relay_rx_bps": 41000000,
      "relay_tx_bps": 298000000,
      "node_rx_bps": 40000000,
      "node_tx_bps": 297000000,
      "relay_rx_bps_max": 60000000,
      "relay_tx_bps_max": 305000000,
      "node_rx_bps_max": 61000000,
      "node_tx_bps_max": 304000000,
      "connections": 1850,
      "cpu": 12.5,
      "v6_rtt_ms": 90.1,
      "v6_loss": 0,
      "v6_loss_max": 0,
      "v6_jitter_ms": 0.12,
      "v4_rtt_ms": 81,
      "v4_loss": 0,
      "v4_loss_max": 0,
      "v4_jitter_ms": 0.08,
      "ha_active": "relay_v6",
      "ha_primary_up": true,
      "ha_backup_up": true,
      "samples": 5
    },
    {
      "t": "2026-10-19T11:42:10Z",
      "relay_rx_bps": null,
      "relay_tx_bps": null,
      "node_rx_bps": null,
      "node_tx_bps": null,
      "relay_rx_bps_max": null,
      "relay_tx_bps_max": null,
      "node_rx_bps_max": null,
      "node_tx_bps_max": null,
      "connections": null,
      "cpu": null,
      "v6_rtt_ms": null,
      "v6_loss": null,
      "v6_loss_max": null,
      "v6_jitter_ms": null,
      "v4_rtt_ms": null,
      "v4_loss": null,
      "v4_loss_max": null,
      "v4_jitter_ms": null,
      "ha_active": null,
      "ha_primary_up": false,
      "ha_backup_up": null,
      "samples": 5
    }
  ],
  "since": "2026-10-18T11:42:10Z",
  "tunnel_id": 3
}
```

---

## 2. Network Doctor (sudo)

A read-only diagnosis run over SSH on the node and the relay with the logins stored on the tunnel row: 100 pings 0.2 s
apart over IPv6 and IPv4 **in both directions**, TCP connects to the relay's control port, a don't-fragment MTU probe, a
hop-by-hop probe (pings with a growing hop limit), network-card drop counters, queue drops, TCP retransmissions, the
HAProxy state, plus the last 24 h of stored samples and events. It takes about 25-30 seconds. State is kept in Redis for
one hour per tunnel.

### `POST /api/tunnels/:id/diagnose`

No body. Starts the job and answers **202** with its state. If one is already running: **409** with
`{"detail": "...", "job": {...}}`.

<!-- example:doctor_running -->
```json
{
  "status": "running",
  "started_at": "2026-10-19T02:16:45.6248568Z"
}
```

### `GET /api/tunnels/:id/diagnose`

The latest job: `status` is `running`, `done` (with `result`) or `failed` (with `error`). **404** when none was run in
the last hour. A `running` job older than about four and a half minutes is reported as `failed` (the panel restarted while it ran). The
result is abbreviated here: lists are cut after a few items; the real response carries all hops, probes and findings.

<!-- example:doctor_job -->
```json
{
  "status": "done",
  "started_at": "2026-10-19T02:16:45.6248568Z",
  "finished_at": "2026-10-19T02:16:45.6253785Z",
  "result": {
    "tunnel": {
      "id": 3,
      "name": "node2",
      "method": "external",
      "protocol": "frp over TCP via HAProxy"
    },
    "started_at": "2026-10-19T02:16:45.6248568Z",
    "finished_at": "2026-10-19T02:16:45.6253785Z",
    "environment": {
      "node_address": "198.51.100.46",
      "relay_address": "203.0.113.44",
      "node_v6": "2001:db8:51::4aa4",
      "node_v4": "198.51.100.46",
      "relay_v6": "2001:db8:3::fe4",
      "relay_v4": "203.0.113.82",
      "control_port": 7000,
      "node_kernel": "6.8.0-31-generic",
      "relay_kernel": "6.8.0-138-generic",
      "node_nic": "eth0",
      "relay_nic": "eth0",
      "ping_count": 100,
      "ping_interval_seconds": 0.2
    },
    "node": {
      "role": "node",
      "reachable": true,
      "kernel": "6.8.0-31-generic",
      "ping": {
        "v6": {
          "target": "2001:db8:3::fe4",
          "sent": 100,
          "received": 100,
          "loss_pct": 0,
          "min_ms": 89.902,
          "avg_ms": 90.022,
          "max_ms": 90.843,
          "jitter_ms": 0.108
        },
        "v4": {
          "target": "203.0.113.82",
          "sent": 100,
          "received": 100,
          "loss_pct": 0,
          "min_ms": 80.983,
          "avg_ms": 81.038,
          "max_ms": 81.719,
          "jitter_ms": 0.077
        }
      },
      "tcp": [
        {
          "family": "v6",
          "address": "2001:db8:3::fe4",
          "port": 7000,
          "attempts": 5,
          "successes": 5,
          "min_ms": 92.56,
          "avg_ms": 93.28059999999999,
          "max_ms": 95.164
        }
      ],
      "mtu": [
        {
          "family": "v6",
          "probes": [
            {
              "payload": 1452,
              "ok": true
            },
            {
              "payload": 1432,
              "ok": true
            },
            {
              "payload": 1392,
              "ok": true
            },
            {
              "payload": 1352,
              "ok": true
            },
            {
              "payload": 1232,
              "ok": true
            }
          ],
          "largest_ok_payload": 1452,
          "path_mtu": 1500
        }
      ],
      "hops": [
        {
          "family": "v6",
          "hops": [
            {
              "ttl": 1,
              "addr": "2a12:de40:51::1",
              "replies": 4,
              "sent": 4,
              "reached": false
            },
            {
              "ttl": 2,
              "addr": "2a12:de40:57::5",
              "replies": 4,
              "sent": 4,
              "reached": false
            },
            {
              "ttl": 3,
              "replies": 0,
              "sent": 4,
              "reached": false
            }
          ]
        }
      ],
      "nic": {
        "name": "eth0",
        "rx_packets": 129238,
        "tx_packets": 87113,
        "rx_errors": 0,
        "rx_dropped": 0,
        "rx_missed": 0,
        "tx_errors": 0,
        "tx_dropped": 0,
        "tx_carrier_errors": 0,
        "qdisc_dropped": 0,
        "total_qdisc_dropped": 158017,
        "total_tx_packets": 3195017996
      },
      "tcp_stats": {
        "out_segs": 284682,
        "retrans_segs": 358,
        "retrans_pct": 0.12575435046824177,
        "total_out_segs": 3223570784,
        "total_retrans_segs": 45252671,
        "total_retrans_pct": 1.403805718323572
      },
      "haproxy": {
        "present": true,
        "active": "relay_v6",
        "servers": [
          {
            "backend": "frp_relay",
            "name": "relay_v6",
            "addr": "2001:db8:3::fe4:7000",
            "status": "UP",
            "backup": false,
            "sessions": 2143,
            "total_sessions": 127259,
            "since_seconds": 102
          },
          {
            "backend": "frp_relay",
            "name": "relay_v4",
            "addr": "203.0.113.82:7000",
            "status": "UP",
            "backup": true,
            "sessions": 1,
            "total_sessions": 15983,
            "since_seconds": 3194
          }
        ]
      }
    },
    "relay": {
      "role": "relay",
      "reachable": true,
      "kernel": "6.8.0-138-generic",
      "ping": {
        "v6": {
          "target": "2001:db8:51::4aa4",
          "sent": 100,
          "received": 100,
          "loss_pct": 0,
          "min_ms": 86.781,
          "avg_ms": 87.061,
          "max_ms": 90.305,
          "jitter_ms": 0.592
        },
        "v4": {
          "target": "198.51.100.46",
          "sent": 100,
          "received": 100,
          "loss_pct": 0,
          "min_ms": 82.361,
          "avg_ms": 82.434,
          "max_ms": 82.673,
          "jitter_ms": 0.048
        }
      },
      "mtu": [
        {
          "family": "v6",
          "probes": [
            {
              "payload": 1452,
              "ok": true
            },
            {
              "payload": 1432,
              "ok": true
            },
            {
              "payload": 1392,
              "ok": true
            },
            {
              "payload": 1352,
              "ok": true
            },
            {
              "payload": 1232,
              "ok": true
            }
          ],
          "largest_ok_payload": 1452,
          "path_mtu": 1500
        }
      ],
      "hops": [
        {
          "family": "v6",
          "hops": [
            {
              "ttl": 1,
              "addr": "2001:db8:3::1",
              "replies": 4,
              "sent": 4,
              "reached": false
            },
            {
              "ttl": 2,
              "addr": "fd1e::51",
              "replies": 4,
              "sent": 4,
              "reached": false
            },
            {
              "ttl": 3,
              "addr": "fd20::21",
              "replies": 4,
              "sent": 4,
              "reached": false
            }
          ]
        }
      ],
      "nic": {
        "name": "eth0",
        "rx_packets": 81163,
        "tx_packets": 77192,
        "rx_errors": 0,
        "rx_dropped": 10,
        "rx_missed": 0,
        "tx_errors": 0,
        "tx_dropped": 0,
        "tx_carrier_errors": 0,
        "qdisc_dropped": 0,
        "total_qdisc_dropped": 0,
        "total_tx_packets": 55738539
      },
      "tcp_stats": {
        "out_segs": 120539,
        "retrans_segs": 1121,
        "retrans_pct": 0.9299894639909075,
        "total_out_segs": 86458764,
        "total_retrans_segs": 733409,
        "total_retrans_pct": 0.8482760637198098
      }
    },
    "history": {
      "window_hours": 24,
      "from": "2026-10-18T02:16:45.6253785Z",
      "to": "2026-10-19T02:16:45.6253785Z",
      "samples": 1440,
      "outages": [
        {
          "at": "2026-10-18T21:16:45.6253785Z",
          "seconds": 120
        },
        {
          "at": "2026-10-19T00:46:00.6253785Z",
          "seconds": 45
        }
      ],
      "outage_count": 2,
      "downtime_seconds": 165,
      "longest_outage_seconds": 120,
      "failovers": 1,
      "failbacks": 1,
      "loss_events": 0,
      "nic_saturated_events": 0,
      "throughput": [
        {
          "series": "relay_tx",
          "label": "relay card, outbound",
          "samples": 1440,
          "p50_mbps": 28.692701191686577,
          "p90_mbps": 301.60191512106053,
          "p99_mbps": 302.9022951785992,
          "max_mbps": 302.99803151441637
        }
      ],
      "plateau": {
        "detected": true,
        "series": "relay_tx",
        "label": "relay card, outbound",
        "level_mbps": 300.1333178145006,
        "windows": 30,
        "windows_total": 72,
        "flat_minutes": 600,
        "near_max_share": 1,
        "ceiling_events": 1
      },
      "loss_by_rate": [
        {
          "from_mbps": 10,
          "to_mbps": 50,
          "samples": 840,
          "v6_loss_pct": 0,
          "v4_loss_pct": 0,
          "v6_rtt_ms": 90,
          "v4_rtt_ms": 81
        },
        {
          "from_mbps": 200,
          "to_mbps": 400,
          "samples": 600,
          "v6_loss_pct": 3.15,
          "v4_loss_pct": 0,
          "v6_rtt_ms": 90,
          "v4_rtt_ms": 81
        }
      ],
      "v6_loss_pct": 1.3125,
      "v4_loss_pct": 0,
      "v6_lossy_share": 0.13125,
      "v4_lossy_share": 0
    },
    "findings": [
      {
        "code": "mtu_node_v4",
        "severity": "warning",
        "title": "Smaller path MTU from the node to the relay over IPv4",
        "detail": "The largest don't-fragment payload that was answered is 1400 bytes (packet size 1428); a standard 1500-byte path answers 1472. Sizes tried: 1472, 1452, 1400, 1372, 1272.",
        "title_fa": "MTU کوچک‌تر در مسیر از نود به رله روی IPv4",
        "detail_fa": "بزرگ‌ترین payload بدون‌تکه‌شدن که پاسخ گرفت 1400 بایت است (اندازهٔ بسته 1428)؛ مسیر استاندارد ۱۵۰۰ بایتی تا 1472 بایت پاسخ می‌دهد. اندازه‌های آزمایش‌شده: 1472, 1452, 1400, 1372, 1272."
      },
      {
        "code": "outages",
        "severity": "warning",
        "title": "The relay stopped answering 2 time(s) in 24 h",
        "detail": "2 outage(s), 165 s in total, the longest 120 s.",
        "title_fa": "رله 2 بار در 24 ساعت پاسخ نداد",
        "detail_fa": "2 قطعی، مجموعاً 165 ثانیه، طولانی‌ترین 120 ثانیه."
      },
      {
        "code": "plateau",
        "severity": "warning",
        "title": "Traffic is pinned flat at one level",
        "detail": "relay card, outbound: 30 of 72 consecutive 20-minute windows were flat within 5% around 300 Mbit/s (about 600 minutes in total). Of the samples above half the maximum, 100% lie within 5% of the maximum. The panel also recorded 1 ceiling event(s).",
        "title_fa": "ترافیک روی یک سطح ثابت گیر کرده است",
        "detail_fa": "relay card, outbound: 30 پنجرهٔ ۲۰ دقیقه‌ای از 72 پنجرهٔ پیاپی با تلورانس ۵٪ روی حدود 300 Mbit/s ثابت بود (جمعاً حدود 600 دقیقه). از نمونه‌های بالای نصف بیشینه، 100٪ در ۵٪ بیشینه قرار دارند. پنل همچنین 1 رویداد سقف پهنای باند ثبت کرده است."
      }
    ]
  }
}
```

Notes on the result

- `node` is the node measuring towards the relay, `relay` the relay measuring towards the node. `ping.v6/v4` are
  `100` pings each; `tcp` is node to relay only. A family with no address is absent.
- `mtu[].largest_ok_payload` is the largest payload that was answered with the don't-fragment bit (1472 / 1452 are a
  standard 1500-byte path for IPv4 / IPv6); `path_mtu` is that plus the IP and ICMP headers.
- `hops[].hops[]`: `replies` of `sent` probes were answered by `addr` (empty = no answer); the list stops at the first hop
  where the destination answered (`reached`).
- `nic` and `tcp_stats` are counter changes during the test window, plus `total_*` since boot.
- `history.plateau` and `history.loss_by_rate` summarise the 24 h of samples. `findings[]` are observations built only from
  the numbers; `title_fa`/`detail_fa` are the Persian wording.
- A diagnosis never changes anything on the servers. The report and the result contain addresses and measurements only,
  never credentials.

### `GET /api/tunnels/:id/diagnose/report`

Query: `lang` = `en` (default) or `fa`; `download=1` adds `Content-Disposition: attachment`.
Returns **Markdown** (`text/markdown; charset=utf-8`) meant for the datacenter's network team: Summary, Environment,
Throughput plateau, Delay and loss inside the datacenter path (ping, TCP connect and MTU tables), Loss versus traffic rate,
Path and hop-by-hop findings, host counters, What we ask the network team to check, How to reproduce. The Persian report
is wrapped in `<div dir="rtl">`. Statements are measurements; possible causes appear only as questions. **404** when no
diagnosis exists, **409** while it runs or if it failed.

---

## 3. Reseller dashboard and user tools

### `GET /api/admins/overview` (admin)

Sudo gets a row per admin (plus a row `"(no owner)"` with `admin_id: null` for configs nobody owns) and `totals`; a
reseller gets only their own row. Counting rules: `expiring_in_3_days` / `expiring_in_7_days` count **active** configs
whose expiry is in the future and within that many days; `low_data` counts **active** configs with a data limit and 10% or
less of it left; `data_limit` is the sum of the limits of the configs that have one (`unlimited_configs` have none);
`online` is configs connected right now (live presence, or the database's `online_at` when presence is not available);
`top_consumers` are the five configs with the most used traffic.

<!-- example:overview -->
```json
{
  "admins": [
    {
      "admin_id": 7,
      "username": "reseller1",
      "is_sudo": false,
      "configs": {
        "total": 120,
        "active": 95,
        "disabled": 5,
        "limited": 8,
        "expired": 10,
        "on_hold": 2
      },
      "used_traffic": 912345678901,
      "data_limit": 3221225472000,
      "unlimited_configs": 4,
      "expiring_in_3_days": 6,
      "expiring_in_7_days": 14,
      "low_data": 9,
      "online": 31,
      "top_consumers": [
        {
          "username": "alice",
          "used_traffic": 48318382080,
          "data_limit": 53687091200
        },
        {
          "username": "bob",
          "used_traffic": 20000000000,
          "data_limit": null
        }
      ]
    }
  ],
  "generated_at": "2026-10-19T11:42:10Z",
  "totals": {
    "admin_id": null,
    "username": "total",
    "is_sudo": false,
    "configs": {
      "total": 120,
      "active": 0,
      "disabled": 0,
      "limited": 0,
      "expired": 0,
      "on_hold": 0
    },
    "used_traffic": 0,
    "data_limit": 0,
    "unlimited_configs": 0,
    "expiring_in_3_days": 0,
    "expiring_in_7_days": 0,
    "low_data": 0,
    "online": 0,
    "top_consumers": []
  }
}
```

### `POST /api/users/bulk` (admin)

Body: exactly one of `usernames` (list) or `filter` (`{"admin": "name", "status": ["active"], "search": "text"}`, the same
selection as `GET /api/users`), plus `action` and, for `extend_days`, `days` (1-3650). At most 500 users per call (a
filter matching more is **422**).

| action | does |
| --- | --- |
| `enable` | disabled -> active. Skipped when the user is not disabled, the data limit is used up, or the expiry has passed (the reason is returned). A reseller stays under the reseller-API active-user cap. |
| `disable` | any status but disabled -> disabled |
| `extend_days` | moves the expiry forward by `days` from the later of the current expiry and now; an expired user comes back (limited if the data is used up). Skipped for users with no expiry or on hold. |
| `reset_usage` | same as `POST /api/user/:username/reset` |
| `delete` | same as `DELETE /api/user/:username` |

Each user goes through the same code as the single-user endpoints: notification reports (so N users send N messages,
honouring the `NOTIFY_*` flags), Gateway sync and node data. A reseller can only touch their own users; others come back
`forbidden` in `results`. Gateway replicas are `skipped`. The HTTP status is 200 even if some users failed; check
`results[].status`: `ok`, `skipped`, `error`, `forbidden`, `not_found`.

<!-- example:bulk -->
```json
{
  "action": "extend_days",
  "total": 4,
  "succeeded": 2,
  "skipped": 1,
  "failed": 1,
  "results": [
    {
      "username": "alice",
      "status": "ok"
    },
    {
      "username": "bob",
      "status": "ok"
    },
    {
      "username": "carol",
      "status": "skipped",
      "detail": "the user has no expiry (unlimited)"
    },
    {
      "username": "mallory",
      "status": "forbidden",
      "detail": "You're not allowed"
    }
  ]
}
```

### `GET /api/users/export` (admin)

Query: `admin` (sudo only), `search`, `status` (repeatable), `sort` - the same as `GET /api/users`, without paging.
Streams `text/csv; charset=utf-8` (with a UTF-8 byte order mark) as an attachment. Columns:
`username,status,used_traffic,data_limit,expire,admin,created_at,note,online` - bytes for traffic and limit (empty limit =
unlimited), RFC 3339 UTC for `expire` (empty = none) and `created_at`, `true`/`false` for `online`. Cells that begin with
`= + - @` are prefixed with `'` so spreadsheets do not run them as formulas.

### Search additions in `GET /api/users` (and the export, and bulk `filter.search`)

`search` already matched username, note and config UID. It now also accepts a **pasted subscription link or token**
(`https://host/sub/<token>`, with or without `/info` or a query, or the bare token): the token is decoded the way the
subscription endpoint does and the user it names is matched exactly.

**Client IP search is not possible:** the panel stores no client addresses. A subscription fetch records only the time and
the `User-Agent`; nodes sit behind the relay and see only its address. See section 6.

### Daily digest

Once a day, from `ADMIN_DIGEST_HOUR_UTC` (default 6), every admin with a `telegram_id` gets **one direct Telegram
message** with the number of their configs expiring within 3 days and with 10% or less data left (nothing is sent when both
are zero). Gated by `NOTIFY_ADMIN_DIGEST` (default true) and the bot token in the integration settings. At most once per
admin per UTC day, across restarts.

---

## 4. Node restart

Nodes poll the panel; there is no channel from the panel to a node, so this goes in over SSH.

### `POST /api/node/:id/restart` (sudo)

Body:

| field | |
| --- | --- |
| `mode` | `"service"` or `"reboot"` (required) |
| `confirm_name` | required for `reboot`: must equal the node's `name` exactly (case-sensitive) |
| `ssh_port`, `ssh_user`, `ssh_password` | only used when no tunnel row stores a login for this node; used for this call only and never stored. A stored login wins. |

Credentials come from the node SSH login on a tunnel row for that node (an active tunnel first); `ssh_known` on
`GET /api/nodes` says whether one exists. If neither exists: **422**. Other errors: **404** unknown node, **422** bad
`mode` or wrong `confirm_name`, **502** SSH could not connect (`detail` says why). A non-root login is used with
`sudo -n`.

`service`: `systemctl restart rapido-go-node`, then polls `systemctl is-active` every 2 s for up to 25 s and looks in the
journal for `pull config: applied` / `hot-applied` after the restart. `ok` is true only when the service is active **and**
it pulled its config again; `active` and `config_pulled` say which part failed. HAProxy, frpc and other services are
separate services and are not touched. The HTTP status is 200 even when `ok` is false.

<!-- example:node_restart -->
```json
{
  "ok": true,
  "mode": "service",
  "detail": "the node agent is active and applied its config again",
  "active": true,
  "config_pulled": true,
  "took_ms": 4210,
  "credentials_from": "tunnel"
}
```

`reboot`: schedules `systemctl reboot` two seconds later, detached, and returns at once.

<!-- example:node_reboot -->
```json
{
  "ok": true,
  "mode": "reboot",
  "detail": "The server will reboot in about two seconds. The node agent, HAProxy, frpc and other services come back by themselves when it is up.",
  "took_ms": 310,
  "credentials_from": "request"
}
```

Either mode adds a `node_restart` / `node_reboot` event to the timeline of every tunnel of that node.

### `GET /api/nodes`: new field `ssh_known`

`ssh_known` (bool) is true when some tunnel row holds that node's SSH login, so the UI knows whether it must ask for a
password before calling restart. Also present on `GET /api/node/:id` and the create/update responses.

<!-- example:node -->
```json
{
  "id": 2,
  "name": "node2",
  "address": "198.51.100.46",
  "port": 62050,
  "api_port": 62051,
  "xray_version": "1.8.0",
  "status": "connected",
  "message": null,
  "usage_coefficient": 1,
  "capacity": null,
  "ssh_known": true
}
```

---

## 5. Config-sharing detection: not built

Investigated; **no endpoint exists** because the data to detect sharing reliably is not collected. What exists: every node
sends the panel, every 5 s, a list of `[username, open connections]` (`POST /api/internal/node-live`), but the panel keeps
only "this user was seen" (the connection count is discarded). A count of open TCP connections cannot tell one heavy device
(a phone with a browser and apps holds dozens of connections) from several people sharing a config, so a threshold on it
would only list heavy single users. Distinct clients cannot be seen: the node only ever sees the relay's address, and the
relay sees real client addresses but not which config they use.

What would be needed: the client's real address at the node, per config. The smallest change is to have frp send a PROXY
protocol header to the node (`transport.proxyProtocolVersion = "v2"` on the frpc proxies), make the node's listeners read
it, count **distinct source addresses per config per window** on the node, and include that count in `node-live`. The panel
could then flag configs with several different addresses connected at the same time. Until then the panel has nothing
honest to report.

## 6. Settings and background jobs

| setting | default | |
| --- | --- | --- |
| `NOTIFY_INFRA_ALERT` | true | also gates the new tunnel event alerts (failover, loss, ceiling, saturation) |
| `NOTIFY_ADMIN_DIGEST` | true | the daily digest |
| `ADMIN_DIGEST_HOUR_UTC` | 6 | hour (UTC) from which the digest goes out |

Background (backend singleton only): `tunnelwatch` stores samples/events each metrics round and prunes samples older than 14
days and events older than 90 days hourly; the digest job checks every 10 minutes. New migration: `00030_tunnel_samples_events.sql`.
