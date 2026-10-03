import { Graph } from './graph.ts';

/**
 * Bron–Kerbosch 算法（带主元剪枝）枚举图中全部极大团。
 *
 * 经典递归形式 BK(R, P, X)：
 *   R — 当前已选入团且两两相邻的顶点；
 *   P — 尚可扩展的候选顶点；
 *   X — 已处理过、用于排除重复的顶点。
 * 主元（pivot）选取 P ∪ X 中在 P 内邻居最多的顶点 u，
 * 只需对 P \ N(u) 递归，可大幅剪枝（最坏情况仍指数，实际很快）。
 */
export function* bronKerbosch(
  graph: Graph,
  candidates?: Iterable<number>,
): Generator<number[]> {
  const P = new Set<number>(candidates ?? graph.vertices());
  if (P.size === 0) return; // 空候选集不产生空团
  yield* bk(graph, [], P, new Set<number>());
}

function* bk(
  graph: Graph,
  R: number[],
  P: Set<number>,
  X: Set<number>,
): Generator<number[]> {
  if (P.size === 0 && X.size === 0) {
    yield [...R];
    return;
  }
  // 选主元：P ∪ X 中使 |P ∩ N(u)| 最大的 u
  let pivot = -1;
  let pivotCover = -1;
  for (const u of union(P, X)) {
    const cover = intersectSize(P, graph.neighbors(u));
    if (cover > pivotCover) {
      pivotCover = cover;
      pivot = u;
    }
  }
  const pivotNs = graph.neighbors(pivot);
  // 仅对 P \ N(pivot) 中的顶点递归
  for (const v of [...P]) {
    if (pivotNs.has(v)) continue;
    const ns = graph.neighbors(v);
    yield* bk(graph, [...R, v], intersect(P, ns), intersect(X, ns));
    P.delete(v);
    X.add(v);
  }
}

function* union(a: Set<number>, b: Set<number>): Generator<number> {
  yield* a;
  yield* b;
}

function intersect(a: Set<number>, b: Set<number>): Set<number> {
  const [small, large] = a.size <= b.size ? [a, b] : [b, a];
  const out = new Set<number>();
  for (const x of small) if (large.has(x)) out.add(x);
  return out;
}

function intersectSize(a: Set<number>, b: Set<number>): number {
  const [small, large] = a.size <= b.size ? [a, b] : [b, a];
  let n = 0;
  for (const x of small) if (large.has(x)) n++;
  return n;
}

/** 便捷封装：返回排序后的极大团列表（每个团内部升序，团之间按字典序）。 */
export function listMaximalCliques(
  graph: Graph,
  candidates?: Iterable<number>,
): number[][] {
  const cliques = [...bronKerbosch(graph, candidates)].map((c) =>
    c.sort((a, b) => a - b),
  );
  cliques.sort((a, b) => {
    for (let i = 0; i < Math.min(a.length, b.length); i++) {
      if (a[i] !== b[i]) return a[i] - b[i];
    }
    return a.length - b.length;
  });
  return cliques;
}
