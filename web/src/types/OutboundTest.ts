// Mirrors internal/httpapi/outboundprobe.go's response to
// POST /api/core/outbounds/test.

/** Why a node measured nothing for an outbound. */
export type OutboundTestSkip = "missing" | "block" | "no_tcp" | "busy";

export type OutboundTestResult = {
  node_id: number;
  node: string;
  tag: string;
  delay_ms: number;
  status_code: number;
  error?: string;
  skipped?: OutboundTestSkip | string;
  /** The interface the outbound is pinned to, and whether that node has it. */
  interface?: string;
  interface_here: boolean;
  /** For an exit with a direct fallback: the member in use, and whether it is the plain direct one. */
  via?: string;
  fallback: boolean;
  /** Set when the outbound was sent whole as a draft and tested alone, rather than by tag. */
  draft?: boolean;
};

export type OutboundTestResponse = {
  url: string;
  nodes: { id: number; name: string; error?: string }[];
  results: OutboundTestResult[];
};
