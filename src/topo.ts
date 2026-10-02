/**
 * 并发事务依赖拓扑排序。
 *
 * 依赖定义：事务 X 与 Y 冲突且 X.seq < Y.seq，则存在边 X -> Y
 * （序号小者必须先于序号大者被裁决）。
 *
 * 使用 Kahn 算法，就绪集以 (seq, txnId) 字典序打破平局，
 * 因此排序结果对同一依赖图完全确定。
 */

export interface TopoNode {
  id: string;
  seq: number;
}

export function topoSort<T extends TopoNode>(
  nodes: readonly T[],
  edges: ReadonlyMap<string, ReadonlySet<string>>,
): T[] {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const indegree = new Map<string, number>();
  for (const n of nodes) indegree.set(n.id, 0);
  for (const [from, tos] of edges) {
    if (!byId.has(from)) continue;
    for (const to of tos) {
      if (byId.has(to)) indegree.set(to, (indegree.get(to) ?? 0) + 1);
    }
  }

  const compare = (a: T, b: T): number =>
    a.seq - b.seq || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);

  const ready: T[] = nodes.filter((n) => (indegree.get(n.id) ?? 0) === 0).sort(compare);
  const result: T[] = [];

  while (ready.length > 0) {
    const node = ready.shift() as T;
    result.push(node);
    for (const to of edges.get(node.id) ?? []) {
      if (!byId.has(to)) continue;
      const d = (indegree.get(to) ?? 0) - 1;
      indegree.set(to, d);
      if (d === 0) {
        const target = byId.get(to) as T;
        // 插入并保持就绪集有序（确定性的关键）。
        let i = ready.length;
        while (i > 0 && compare(ready[i - 1], target) > 0) i--;
        ready.splice(i, 0, target);
      }
    }
  }

  if (result.length !== nodes.length) {
    // 依赖图由 (seq 小 -> seq 大) 构成，理论上无环；防御性检查。
    throw new Error('dependency cycle detected: non-deterministic input');
  }
  return result;
}
