import type { CausalEdge, TraceEvent } from './types.ts';

/**
 * Builds the happened-before (Lamport) partial-order graph:
 *  - process edges: consecutive events on the same server, ordered by local
 *    physical time (ties broken by ingestion order);
 *  - message edges: REQ_SEND -> REQ_RECV and RESP_SEND -> RESP_RECV,
 *    matched by rpcId.
 */
export function buildCausalGraph(events: readonly TraceEvent[]): {
  edges: CausalEdge[];
} {
  const edges: CausalEdge[] = [];

  const byServer = new Map<string, { event: TraceEvent; seq: number }[]>();
  events.forEach((event, seq) => {
    const list = byServer.get(event.server) ?? [];
    list.push({ event, seq });
    byServer.set(event.server, list);
  });
  for (const list of byServer.values()) {
    list.sort((a, b) => a.event.time - b.event.time || a.seq - b.seq);
    for (let i = 1; i < list.length; i++) {
      edges.push({ from: list[i - 1].event.id, to: list[i].event.id, kind: 'process' });
    }
  }

  const byRpc = new Map<string, TraceEvent[]>();
  for (const event of events) {
    const list = byRpc.get(event.rpcId) ?? [];
    list.push(event);
    byRpc.set(event.rpcId, list);
  }
  for (const rpcEvents of byRpc.values()) {
    const find = (kind: TraceEvent['kind']) => rpcEvents.find((e) => e.kind === kind);
    const pairs: [TraceEvent | undefined, TraceEvent | undefined][] = [
      [find('REQ_SEND'), find('REQ_RECV')],
      [find('RESP_SEND'), find('RESP_RECV')],
    ];
    for (const [send, recv] of pairs) {
      if (send && recv) {
        edges.push({ from: send.id, to: recv.id, kind: 'message' });
      }
    }
  }

  return { edges };
}

/** Kahn's algorithm; deterministic (stable by input event order). */
export function topologicalOrder(
  events: readonly TraceEvent[],
  edges: readonly CausalEdge[],
): string[] {
  const indegree = new Map<string, number>(events.map((e) => [e.id, 0]));
  const outgoing = new Map<string, string[]>();
  for (const edge of edges) {
    indegree.set(edge.to, (indegree.get(edge.to) ?? 0) + 1);
    const list = outgoing.get(edge.from) ?? [];
    list.push(edge.to);
    outgoing.set(edge.from, list);
  }
  const queue = events.filter((e) => indegree.get(e.id) === 0).map((e) => e.id);
  const order: string[] = [];
  while (queue.length > 0) {
    const id = queue.shift()!;
    order.push(id);
    for (const next of outgoing.get(id) ?? []) {
      const remaining = indegree.get(next)! - 1;
      indegree.set(next, remaining);
      if (remaining === 0) queue.push(next);
    }
  }
  if (order.length !== events.length) {
    throw new Error('causal graph contains a cycle; input log is not causally consistent');
  }
  return order;
}
