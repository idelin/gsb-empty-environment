import { buildCausalGraph, topologicalOrder } from './graph.ts';
import { estimateClockOffsets, DEFAULT_MIN_LATENCY_MS } from './corrector.ts';
import type {
  CausalEdge,
  ClockCorrection,
  CorrectedEvent,
  LatencyViolation,
  Topology,
  TraceEvent,
} from './types.ts';

export interface ReconstructOptions {
  /** Minimum admissible wire latency for any RPC hop, in ms. */
  minLatencyMs?: number;
}

function detectViolations(
  events: readonly TraceEvent[],
  edges: readonly CausalEdge[],
): LatencyViolation[] {
  const byId = new Map(events.map((e) => [e.id, e]));
  const violations: LatencyViolation[] = [];
  for (const edge of edges) {
    if (edge.kind !== 'message') continue;
    const send = byId.get(edge.from)!;
    const recv = byId.get(edge.to)!;
    const latencyMs = recv.time - send.time;
    if (latencyMs < 0) {
      violations.push({
        rpcId: send.rpcId,
        fromServer: send.server,
        toServer: recv.server,
        sendTime: send.time,
        recvTime: recv.time,
        latencyMs,
      });
    }
  }
  return violations;
}

/**
 * Reconstructs a physically self-consistent causal topology from raw
 * distributed trace events whose server clocks may drift arbitrarily.
 *
 * Pipeline: build happened-before graph -> detect negative-latency
 * counterexamples -> solve per-server clock offsets from causal constraints
 * -> re-stamp every event -> emit corrected topology with a topological
 * order of the partial-order graph.
 */
export function reconstruct(
  events: readonly TraceEvent[],
  options: ReconstructOptions = {},
): Topology {
  const minLatencyMs = options.minLatencyMs ?? DEFAULT_MIN_LATENCY_MS;
  const { edges } = buildCausalGraph(events);
  const violations = detectViolations(events, edges);
  const offsets = estimateClockOffsets(events, edges, minLatencyMs);

  const corrected: CorrectedEvent[] = events.map((event) => ({
    ...event,
    correctedTime: event.time + offsets.get(event.server)!,
  }));

  const corrections: ClockCorrection[] = [...offsets.entries()]
    .map(([server, offsetMs]) => ({ server, offsetMs }))
    .sort((a, b) => a.server.localeCompare(b.server));

  return {
    events: corrected,
    edges,
    corrections,
    order: topologicalOrder(events, edges),
    violations,
  };
}
