import type { CausalEdge, TraceEvent } from './types.ts';

export const DEFAULT_MIN_LATENCY_MS = 1;

/**
 * Estimates one clock offset per server from difference constraints derived
 * from message edges. For an edge send(u@A) -> recv(v@B) with minimum wire
 * latency L:
 *
 *   v.time + offset[B] >= u.time + offset[A] + L
 *   =>  offset[B] >= offset[A] + u.time + L - v.time
 *
 * Offsets are lower bounds, so the minimal physically consistent solution is
 * the least fixpoint, computed by Bellman-Ford style relaxation. Receivers of
 * impossible messages get their clock baseline pushed forward; senders are
 * never moved backward, which keeps the correction minimal and deterministic.
 */
export function estimateClockOffsets(
  events: readonly TraceEvent[],
  edges: readonly CausalEdge[],
  minLatencyMs: number = DEFAULT_MIN_LATENCY_MS,
): Map<string, number> {
  const byId = new Map(events.map((e) => [e.id, e]));
  const servers = [...new Set(events.map((e) => e.server))];
  const offset = new Map<string, number>(servers.map((s) => [s, 0]));

  const messageEdges = edges
    .filter((e) => e.kind === 'message')
    .map((e) => ({ send: byId.get(e.from)!, recv: byId.get(e.to)! }));

  // At most |servers| - 1 relaxation rounds are needed for a DAG of
  // constraints; one extra round detects non-convergence defensively.
  for (let round = 0; round <= servers.length; round++) {
    let changed = false;
    for (const { send, recv } of messageEdges) {
      const required =
        offset.get(send.server)! + send.time + minLatencyMs - recv.time;
      if (required > offset.get(recv.server)!) {
        offset.set(recv.server, required);
        changed = true;
      }
    }
    if (!changed) return offset;
    if (round === servers.length) {
      throw new Error('clock constraints failed to converge; cyclic causality in log');
    }
  }
  return offset;
}
