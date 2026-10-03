import { Graph } from './graph.ts';

/** 团的规范化键：节点升序拼接，用于集合去重与差异比较。 */
export function cliqueKey(clique: readonly number[]): string {
  return [...clique].sort((a, b) => a - b).join(',');
}

/**
 * Bron-Kerbosch 算法（带主元剪枝，Tomita 变体）枚举全部极大团。
 *
 * 递推不变式：R 为当前部分团，P 为可扩展候选，X 为已处理（应排除）节点。
 * 主元 u 取自 P ∪ X 中 |P ∩ N(u)| 最大者，只需对 P \ N(u) 中的节点递归，
 * 因为经由 u 的邻居扩展出的极大团必已在其它分支覆盖。
 */
export function maximalCliques(graph: Graph): number[][] {
  const result: number[][] = [];
  const p = new Set(graph.nodes());
  const x = new Set<number>();
  bronKerboschPivot(graph, [], p, x, result);
  return result;
}

function bronKerboschPivot(
  graph: Graph,
  r: number[],
  p: Set<number>,
  x: Set<number>,
  out: number[][],
): void {
  if (p.size === 0 && x.size === 0) {
    out.push([...r].sort((a, b) => a - b));
    return;
  }
  // 选择主元：使 P ∩ N(u) 最大的 u ∈ P ∪ X
  let pivot = -1;
  let pivotCoverage = -1;
  for (const u of unionIter(p, x)) {
    let coverage = 0;
    for (const w of graph.neighbors(u)) if (p.has(w)) coverage++;
    if (coverage > pivotCoverage) {
      pivotCoverage = coverage;
      pivot = u;
    }
  }
  // 仅对 P \ N(pivot) 中的候选递归
  const pivotNeighbors = graph.neighbors(pivot);
  const candidates: number[] = [];
  for (const v of p) if (!pivotNeighbors.has(v)) candidates.push(v);

  for (const v of candidates) {
    const vNeighbors = graph.neighbors(v);
    const nextP = intersect(p, vNeighbors);
    const nextX = intersect(x, vNeighbors);
    r.push(v);
    bronKerboschPivot(graph, r, nextP, nextX, out);
    r.pop();
    p.delete(v);
    x.add(v);
  }
}

function* unionIter(a: Set<number>, b: Set<number>): IterableIterator<number> {
  yield* a;
  yield* b;
}

function intersect(a: ReadonlySet<number>, b: ReadonlySet<number>): Set<number> {
  const [small, large] = a.size <= b.size ? [a, b] : [b, a];
  const out = new Set<number>();
  for (const v of small) if (large.has(v)) out.add(v);
  return out;
}
