import { Cycle, TxnId, WaitEdge } from './types';

/**
 * Find the shortest directed cycle (fewest edges) in the wait-for graph.
 * BFS from every node back to itself; returns null when the graph is acyclic.
 */
export function findShortestCycle(edges: readonly WaitEdge[]): Cycle | null {
  const adj = new Map<TxnId, WaitEdge[]>();
  const nodes = new Set<TxnId>();
  for (const e of edges) {
    const list = adj.get(e.from) ?? [];
    list.push(e);
    adj.set(e.from, list);
    nodes.add(e.from);
    nodes.add(e.to);
  }

  let best: Cycle | null = null;

  for (const start of nodes) {
    if (best && best.nodes.length <= 2) break; // cannot beat a 2-cycle
    const depth = new Map<TxnId, number>([[start, 0]]);
    const parent = new Map<TxnId, { prev: TxnId; edge: WaitEdge }>();
    const queue: TxnId[] = [start];

    while (queue.length > 0) {
      const node = queue.shift()!;
      const d = depth.get(node)!;
      if (best && d + 1 >= best.nodes.length) continue; // cannot improve
      for (const edge of adj.get(node) ?? []) {
        if (edge.to === start) {
          // Reconstruct path start -> ... -> node, then close with `edge`.
          const path: TxnId[] = [];
          const cycleEdges: WaitEdge[] = [];
          let cur = node;
          while (cur !== start) {
            const p = parent.get(cur)!;
            path.unshift(cur);
            cycleEdges.unshift(p.edge);
            cur = p.prev;
          }
          cycleEdges.push(edge);
          const cycle: Cycle = { nodes: [start, ...path], edges: cycleEdges };
          if (!best || cycle.nodes.length < best.nodes.length) best = cycle;
          continue;
        }
        if (!depth.has(edge.to)) {
          depth.set(edge.to, d + 1);
          parent.set(edge.to, { prev: node, edge });
          queue.push(edge.to);
        }
      }
    }
  }
  return best;
}
