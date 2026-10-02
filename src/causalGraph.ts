import type { CausalEdge, CorrectedEvent, Message } from './types.ts';

/**
 * 构建因果偏序图的边集：
 *  1. message 边：send -> recv（跨服务器因果）；
 *  2. local-order 边：同一服务器上按（校正后）时间相邻的事件连边
 *     （同一时钟域内物理时序即因果序）。
 */
export function buildCausalEdges(
  events: CorrectedEvent[],
  messages: Message[],
): CausalEdge[] {
  const edges: CausalEdge[] = [];
  for (const m of messages) {
    edges.push({ fromEventId: m.sendEventId, toEventId: m.recvEventId, kind: 'message' });
  }

  const byServer = new Map<string, CorrectedEvent[]>();
  for (const e of events) {
    const list = byServer.get(e.server) ?? [];
    list.push(e);
    byServer.set(e.server, list);
  }
  for (const list of byServer.values()) {
    const sorted = [...list].sort((a, b) => a.correctedTimeMs - b.correctedTimeMs);
    for (let i = 0; i + 1 < sorted.length; i++) {
      edges.push({
        fromEventId: sorted[i].id,
        toEventId: sorted[i + 1].id,
        kind: 'local-order',
      });
    }
  }
  return edges;
}

/** Kahn 拓扑排序；若因果图存在环则抛出（说明约束求解遗漏了矛盾）。 */
export function topologicalSort(eventIds: string[], edges: CausalEdge[]): string[] {
  const indegree = new Map<string, number>();
  const adjacency = new Map<string, string[]>();
  for (const id of eventIds) {
    indegree.set(id, 0);
    adjacency.set(id, []);
  }
  for (const e of edges) {
    adjacency.get(e.fromEventId)?.push(e.toEventId);
    indegree.set(e.toEventId, (indegree.get(e.toEventId) ?? 0) + 1);
  }

  const queue = eventIds.filter((id) => indegree.get(id) === 0);
  const order: string[] = [];
  while (queue.length > 0) {
    const id = queue.shift() as string;
    order.push(id);
    for (const next of adjacency.get(id) ?? []) {
      const d = (indegree.get(next) as number) - 1;
      indegree.set(next, d);
      if (d === 0) queue.push(next);
    }
  }
  if (order.length !== eventIds.length) {
    throw new Error('因果图存在环，无法给出拓扑序');
  }
  return order;
}
