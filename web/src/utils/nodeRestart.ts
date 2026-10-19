import { NodeRestartMode, NodeStatus } from "types/Node";

/** How long after a reboot the panel keeps waiting for the node to report back. */
export const REBOOT_TIMEOUT_MS = 5 * 60 * 1000;
/** A node that reports connected sooner than this after the command is still the old session. */
export const REBOOT_MIN_WAIT_MS = 15 * 1000;

export type RebootWait = "waiting" | "back" | "timeout";

/**
 * Where a reboot stands. The node is back when it reports connected AND either
 * it was seen going down first, or enough time has passed that the command
 * cannot still be in flight (a poll that lands before the machine has dropped
 * would otherwise read "connected" and end the wait at once).
 */
export const rebootWaitState = (args: {
  elapsedMs: number;
  status: NodeStatus | undefined;
  sawDown: boolean;
  timeoutMs?: number;
  minWaitMs?: number;
}): RebootWait => {
  const { elapsedMs, status, sawDown, timeoutMs = REBOOT_TIMEOUT_MS, minWaitMs = REBOOT_MIN_WAIT_MS } = args;
  if (status === "connected" && (sawDown || elapsedMs >= minWaitMs)) return "back";
  if (elapsedMs >= timeoutMs) return "timeout";
  return "waiting";
};

/** Reboot needs the node's exact name typed; the agent restart needs nothing. */
export const restartConfirmed = (mode: NodeRestartMode, nodeName: string, typed: string): boolean =>
  mode === "service" || typed === nodeName;

export const formatTook = (ms: number | undefined): string =>
  typeof ms === "number" && Number.isFinite(ms) ? `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s` : "";
