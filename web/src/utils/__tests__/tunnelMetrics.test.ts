import { describe, expect, it } from "vitest";
import { HaproxyServer } from "types/Tunnel";
import {
  autoMax,
  failoverInEffect,
  formatBitrate,
  formatBitrateText,
  formatDuration,
  haproxyStatus,
  isActiveServer,
  lossTotal,
  nextPeak,
  niceCeil,
  nicSpeedBps,
} from "../tunnelMetrics";

describe("formatDuration", () => {
  it("matches Go's Duration.String for whole seconds", () => {
    expect(formatDuration(0)).toBe("0s");
    expect(formatDuration(76)).toBe("1m16s");
    expect(formatDuration(3725)).toBe("1h2m5s");
    expect(formatDuration(-4)).toBe("0s");
  });
});

describe("formatBitrate", () => {
  it("picks the unit and keeps three significant digits at most", () => {
    expect(formatBitrate(0)).toEqual({ value: "0", unit: "bps" });
    expect(formatBitrate(950)).toEqual({ value: "950", unit: "bps" });
    expect(formatBitrate(12_500)).toEqual({ value: "12.5", unit: "kbps" });
    expect(formatBitrate(12_500_000)).toEqual({ value: "12.5", unit: "Mbps" });
    expect(formatBitrate(250_000_000)).toEqual({ value: "250", unit: "Mbps" });
    expect(formatBitrate(1_250_000_000)).toEqual({ value: "1.25", unit: "Gbps" });
    expect(formatBitrateText(2_000_000)).toBe("2 Mbps");
  });
});

describe("auto scale", () => {
  it("rounds up to a human scale", () => {
    expect(niceCeil(0.7)).toBe(1);
    expect(niceCeil(1.2)).toBe(2);
    expect(niceCeil(2.3)).toBe(2.5);
    expect(niceCeil(3)).toBe(5);
    expect(niceCeil(7)).toBe(10);
    expect(niceCeil(130e6)).toBe(200e6);
    expect(niceCeil(0)).toBe(1);
    expect(niceCeil(Number.NaN)).toBe(1);
  });

  it("takes a new maximum at once and forgets an old one slowly", () => {
    expect(nextPeak(10, 50)).toBe(50);
    const after = nextPeak(100, 0);
    expect(after).toBeLessThan(100);
    expect(after).toBeGreaterThan(90);
    let p = 100;
    for (let i = 0; i < 400; i++) p = nextPeak(p, 0);
    expect(p).toBeLessThan(1);
  });

  it("gives headroom over the peak and never goes below the floor", () => {
    expect(autoMax(80e6, 1e6)).toBe(100e6);
    expect(autoMax(0, 1e6)).toBe(1e6);
    expect(autoMax(500_000, 1e6)).toBe(1e6);
  });
});

describe("nicSpeedBps", () => {
  it("converts Mbps to bps and treats virtual-card values as unknown", () => {
    expect(nicSpeedBps(1000)).toBe(1e9);
    expect(nicSpeedBps(-1)).toBeNull();
    expect(nicSpeedBps(0)).toBeNull();
    expect(nicSpeedBps(undefined)).toBeNull();
  });
});

describe("lossTotal", () => {
  it("adds drops and errors in both directions", () => {
    expect(lossTotal({ rx_dropped: 1, tx_dropped: 2, rx_errors: 3, tx_errors: 4 })).toBe(10);
  });
});

describe("haproxyStatus", () => {
  it("colours each HAProxy status word", () => {
    expect(haproxyStatus("UP")).toEqual({ kind: "up", tone: "green" });
    expect(haproxyStatus("up")).toEqual({ kind: "up", tone: "green" });
    expect(haproxyStatus("UP 1/2")).toEqual({ kind: "degraded", tone: "yellow" });
    expect(haproxyStatus("DOWN")).toEqual({ kind: "down", tone: "red" });
    expect(haproxyStatus("DOWN 1/3")).toEqual({ kind: "down", tone: "red" });
    expect(haproxyStatus("MAINT")).toEqual({ kind: "maint", tone: "gray" });
    expect(haproxyStatus("NOLB")).toEqual({ kind: "maint", tone: "gray" });
    expect(haproxyStatus("")).toEqual({ kind: "unknown", tone: "gray" });
  });
});

describe("active server", () => {
  const server = (over: Partial<HaproxyServer>): HaproxyServer => ({
    backend: "relay",
    name: "primary",
    status: "UP",
    backup: false,
    sessions: 0,
    total_sessions: 0,
    since_seconds: 0,
    ...over,
  });

  it("matches by server name or backend/server", () => {
    expect(isActiveServer({ present: true, active: "primary" }, server({}))).toBe(true);
    expect(isActiveServer({ present: true, active: "relay/primary" }, server({}))).toBe(true);
    expect(isActiveServer({ present: true, active: "other" }, server({}))).toBe(false);
    expect(isActiveServer({ present: true }, server({}))).toBe(false);
  });

  it("reports a failover only when a backup server carries the traffic", () => {
    const servers = [server({}), server({ name: "backup4", backup: true })];
    expect(failoverInEffect({ present: true, active: "primary", servers })).toBe(false);
    expect(failoverInEffect({ present: true, active: "backup4", servers })).toBe(true);
    expect(failoverInEffect({ present: true, servers })).toBe(false);
  });
});
