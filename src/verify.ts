import { comparePathKey, type Adjacency, type PathKey } from "./spf.ts";

function collectNodes(adj: Adjacency, source: string): string[] {
  const set = new Set<string>([source]);
  for (const [src, row] of adj) {
    set.add(src);
    for (const dst of row.keys()) set.add(dst);
  }
  return [...set];
}

/**
 * 独立实现一：Bellman-Ford，仅核对距离（不可达不出现在结果中）。
 * 权重非负，不存在负环，|V|-1 轮松弛即收敛。
 */
export function bellmanFordDistances(adj: Adjacency, source: string): Map<string, number> {
  const nodes = collectNodes(adj, source);
  const dist = new Map<string, number>();
  dist.set(source, 0);
  for (let round = 0; round < nodes.length - 1; round++) {
    let changed = false;
    for (const [src, row] of adj) {
      const d = dist.get(src);
      if (d === undefined) continue;
      for (const [dst, edge] of row) {
        const cand = d + edge.weight;
        const old = dist.get(dst);
        if (old === undefined || cand < old) {
          dist.set(dst, cand);
          changed = true;
        }
      }
    }
    if (!changed) break;
  }
  return dist;
}

/**
 * 独立实现二：穷举所有简单路径，按 (距离, 跳数, 路径字典序) 取最优。
 * 只用于小规模图（节点数 <= maxNodes），指数复杂度。
 * 零权环不影响正确性：绕环路径跳数更多，决胜规则下必然更差。
 */
export function exhaustiveBestPaths(
  adj: Adjacency,
  source: string,
  maxNodes = 10,
): Map<string, PathKey> {
  const nodes = collectNodes(adj, source);
  if (nodes.length > maxNodes)
    throw new Error(`图太大（${nodes.length} 节点），拒绝穷举`);
  const best = new Map<string, PathKey>();
  best.set(source, { dist: 0, hops: 0, path: [source] });
  const visited = new Set<string>([source]);
  const dfs = (node: string, dist: number, path: string[]): void => {
    for (const [dst, edge] of adj.get(node) ?? []) {
      if (visited.has(dst)) continue; // 只看简单路径
      const cand: PathKey = {
        dist: dist + edge.weight,
        hops: path.length,
        path: [...path, dst],
      };
      const old = best.get(dst);
      if (!old || comparePathKey(cand, old) < 0) best.set(dst, cand);
      visited.add(dst);
      dfs(dst, cand.dist, cand.path);
      visited.delete(dst);
    }
  };
  dfs(source, 0, [source]);
  return best;
}
