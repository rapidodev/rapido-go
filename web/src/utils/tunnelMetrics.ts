import { BadgeTone } from "rapido-ui/Badge";
import { HaproxyInfo, HaproxyServer, TunnelMetrics } from "types/Tunnel";

// formatDuration renders a second count the same way Go's own
// time.Duration.String() does for the whole-second durations the tunnel
// screens deal in (e.g. 76 -> "1m16s") - matching exactly what an InfraAlert
// recovery message already reports, so the dashboard and the bot never
// disagree on how a downtime is phrased.
export const formatDuration = (totalSeconds: number): string => {
  const s = Math.max(0, Math.round(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h > 0) return `${h}h${m}m${sec}s`;
  if (m > 0) return `${m}m${sec}s`;
  return `${sec}s`;
};

const trim = (n: number, decimals: number) => String(parseFloat(n.toFixed(decimals)));

/** A bit rate split into its number and unit, so a gauge can show the number
 * large and the unit small: 12_500_000 -> { value: "12.5", unit: "Mbps" }. */
export const formatBitrate = (bps: number): { value: string; unit: string } => {
  const v = Math.max(0, bps);
  const pick = (div: number, unit: string) => {
    const n = v / div;
    return { value: trim(n, n >= 100 ? 0 : n >= 10 ? 1 : 2), unit };
  };
  if (v >= 1e9) return pick(1e9, "Gbps");
  if (v >= 1e6) return pick(1e6, "Mbps");
  if (v >= 1e3) return pick(1e3, "kbps");
  return { value: trim(v, 0), unit: "bps" };
};

export const formatBitrateText = (bps: number): string => {
  const { value, unit } = formatBitrate(bps);
  return `${value} ${unit}`;
};

/** Rounds up to 1, 2, 2.5, 5 or 10 times a power of ten - a scale a person
 * would pick, so an auto-ranged dial gets round tick labels. */
export const niceCeil = (v: number): number => {
  if (!(v > 0) || !Number.isFinite(v)) return 1;
  const exp = Math.floor(Math.log10(v));
  const base = Math.pow(10, exp);
  for (const m of [1, 2, 2.5, 5, 10]) if (v <= m * base * (1 + 1e-9)) return m * base;
  return 10 * base;
};

/** The highest value seen, but forgetting slowly: a new maximum is taken at
 * once, otherwise the old one decays by `decay` per sample toward the
 * current reading, so a one-off spike does not pin the scale forever. */
export const nextPeak = (previous: number, value: number, decay = 0.97): number =>
  Math.max(value, previous * decay + value * (1 - decay));

/** The dial maximum for an auto-scaled reading: headroom over the peak, rounded to a nice number. */
export const autoMax = (peak: number, floor: number, headroom = 1.25): number =>
  niceCeil(Math.max(peak * headroom, floor));

/** NIC speed in bits per second, or null when it is unknown. Virtual cards
 * report -1 or 0, which means "not known", not "zero speed". */
export const nicSpeedBps = (mbps: number | null | undefined): number | null =>
  typeof mbps === "number" && Number.isFinite(mbps) && mbps > 0 ? mbps * 1e6 : null;

export const isNumber = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

/** Cumulative dropped + errored packets on the tunnel interface. */
export const lossTotal = (m: Pick<TunnelMetrics, "rx_dropped" | "tx_dropped" | "rx_errors" | "tx_errors">): number =>
  (m.rx_dropped || 0) + (m.tx_dropped || 0) + (m.rx_errors || 0) + (m.tx_errors || 0);

export type HaproxyStatusKind = "up" | "degraded" | "down" | "maint" | "unknown";

/** HAProxy's own status words -> a colour. "UP" green; "UP 1/2" (still up but
 * one of its health checks failing) amber; "DOWN" red; "MAINT"/"NOLB"/"DRAIN"
 * grey. */
export const haproxyStatus = (status: string): { kind: HaproxyStatusKind; tone: BadgeTone } => {
  const s = (status || "").trim().toUpperCase();
  if (s.startsWith("UP")) {
    return /\d+\s*\/\s*\d+/.test(s) ? { kind: "degraded", tone: "yellow" } : { kind: "up", tone: "green" };
  }
  if (s.startsWith("DOWN")) return { kind: "down", tone: "red" };
  if (s.startsWith("MAINT") || s.startsWith("NOLB") || s.startsWith("DRAIN")) return { kind: "maint", tone: "gray" };
  return { kind: "unknown", tone: "gray" };
};

/** Does HAProxy's `active` (a server name, or "backend/server") name this server? */
export const isActiveServer = (info: HaproxyInfo, server: HaproxyServer): boolean => {
  const a = info.active;
  if (!a) return false;
  return a === server.name || a === `${server.backend}/${server.name}`;
};

/** True when traffic is on a backup path - i.e. a failover has happened. */
export const failoverInEffect = (info: HaproxyInfo): boolean =>
  (info.servers ?? []).some((s) => s.backup && isActiveServer(info, s));

/** Which IP family an address (with or without a port, bracketed or not) is in; null for a hostname. */
export const ipFamily = (addr: string | null | undefined): "IPv6" | "IPv4" | null => {
  if (!addr) return null;
  const a = addr.trim();
  if (a.startsWith("[") || (a.match(/:/g) ?? []).length >= 2) return "IPv6";
  if (/^\d{1,3}(\.\d{1,3}){3}(:\d+)?$/.test(a)) return "IPv4";
  return null;
};

/** The server HAProxy reports as carrying traffic, if it can be identified. */
export const activeServerOf = (info: HaproxyInfo): HaproxyServer | undefined =>
  (info.servers ?? []).find((s) => isActiveServer(info, s));
