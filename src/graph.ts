/**
 * 带节点颜色的无向多重图。
 * adj[u][v] 表示 u-v 之间平行边的条数（对称矩阵），adj[v][v] 为自环条数。
 */
export interface ColoredMultigraph {
  vertexCount: number;
  colors: number[];
  adj: number[][];
}

export function validateGraph(g: ColoredMultigraph): void {
  const n = g.vertexCount;
  if (!Number.isInteger(n) || n < 0) throw new Error("vertexCount must be a non-negative integer");
  if (!Array.isArray(g.colors) || g.colors.length !== n) throw new Error("colors length mismatch");
  if (!Array.isArray(g.adj) || g.adj.length !== n) throw new Error("adj row count mismatch");
  for (let i = 0; i < n; i++) {
    if (!Number.isInteger(g.colors[i])) throw new Error("colors must be integers");
    if (!Array.isArray(g.adj[i]) || g.adj[i].length !== n) throw new Error("adj must be n x n");
    for (let j = 0; j < n; j++) {
      const m = g.adj[i][j];
      if (!Number.isInteger(m) || m < 0) throw new Error("multiplicities must be non-negative integers");
      if (g.adj[i][j] !== g.adj[j][i]) throw new Error("adj must be symmetric (undirected graph)");
    }
  }
}

/** 边总数（上三角含对角线求和，自环计 1 条）。 */
export function edgeCount(g: ColoredMultigraph): number {
  let total = 0;
  for (let i = 0; i < g.vertexCount; i++) {
    for (let j = i; j < g.vertexCount; j++) total += g.adj[i][j];
  }
  return total;
}

/**
 * 按排列 perm 重标号：返回的图 h 满足 h.adj[i][j] === g.adj[perm[i]][perm[j]]，
 * h.colors[i] === g.colors[perm[i]]。perm 必须是 0..n-1 的排列。
 */
export function permuteGraph(g: ColoredMultigraph, perm: number[]): ColoredMultigraph {
  const n = g.vertexCount;
  const colors = new Array<number>(n);
  const adj: number[][] = Array.from({ length: n }, () => new Array<number>(n).fill(0));
  for (let i = 0; i < n; i++) {
    colors[i] = g.colors[perm[i]];
    for (let j = 0; j < n; j++) adj[i][j] = g.adj[perm[i]][perm[j]];
  }
  return { vertexCount: n, colors, adj };
}

export function sortedCopy(xs: number[]): number[] {
  return xs.slice().sort((a, b) => a - b);
}
