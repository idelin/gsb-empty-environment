/** 有向图：键为模块键 "name@version"，边表示依赖方向（依赖者 -> 被依赖者）。 */
export type Graph = Map<string, ReadonlySet<string>>;

function reconstruct(prev: Map<string, string | null>, node: string, start: string): string[] {
  const path: string[] = [node];
  let cur = node;
  while (cur !== start) {
    const p = prev.get(cur);
    if (p === null || p === undefined) break;
    cur = p;
    path.unshift(cur);
  }
  return path;
}

function depthOf(prev: Map<string, string | null>, node: string, start: string): number {
  let d = 0;
  let cur = node;
  while (cur !== start) {
    const p = prev.get(cur);
    if (p === null || p === undefined) break;
    cur = p;
    d += 1;
  }
  return d;
}

/** 全图最短环（BFS，逐起点剪枝）。返回如 ["a@1", "b@1", "a@1"]，无环返回 null。 */
export function shortestCycle(graph: Graph): string[] | null {
  let best: string[] | null = null;
  for (const start of graph.keys()) {
    const prev = new Map<string, string | null>();
    prev.set(start, null);
    const queue: string[] = [start];
    while (queue.length > 0) {
      const node = queue.shift() as string;
      const depth = depthOf(prev, node, start);
      if (best !== null && depth + 1 >= best.length) continue;
      for (const next of graph.get(node) ?? []) {
        if (next === start) {
          const cycle = [...reconstruct(prev, node, start), start];
          if (best === null || cycle.length < best.length) best = cycle;
        } else if (!prev.has(next)) {
          prev.set(next, node);
          queue.push(next);
        }
      }
    }
  }
  return best;
}

/** Kahn 拓扑排序：边 a->b 表示 a 依赖 b，b 先构建。假定无环（调用前已检测）。 */
export function topoSort(nodes: string[], deps: Graph): string[] {
  const nodeSet = new Set(nodes);
  const remaining = new Map<string, Set<string>>();
  for (const n of nodes) {
    remaining.set(
      n,
      new Set([...(deps.get(n) ?? [])].filter((d) => d !== n && nodeSet.has(d))),
    );
  }
  const order: string[] = [];
  let progressed = true;
  while (remaining.size > 0 && progressed) {
    progressed = false;
    for (const [n, deps_] of remaining) {
      if (deps_.size === 0) {
        order.push(n);
        remaining.delete(n);
        for (const other of remaining.values()) other.delete(n);
        progressed = true;
      }
    }
  }
  if (remaining.size > 0) {
    throw new Error(`拓扑排序失败，存在未检测到的环: ${[...remaining.keys()].join(", ")}`);
  }
  return order;
}
