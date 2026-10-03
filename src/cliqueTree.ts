import { cliqueKey } from './bronKerbosch.ts';

export interface CliqueTreeEdge {
  /** 两端极大团在 nodes 中的下标。 */
  a: number;
  b: number;
  /** 边权 = 两团交集大小（分离集大小）。 */
  weight: number;
}

export interface CliqueTree {
  /** 团树节点：全部极大团。 */
  nodes: number[][];
  /** 最大生成树的边。 */
  edges: CliqueTreeEdge[];
}

/**
 * 团树分解器。
 *
 * 以全部极大团为顶点构建完全团图，边权为两团交集大小，
 * 再用 Kruskal 最大生成树得到团树（junction tree）。
 * 对弦图，该树满足运行交性质：任意图节点 v 所在的团在树中构成连通子树。
 */
export function decomposeCliqueTree(cliques: ReadonlyArray<readonly number[]>): CliqueTree {
  const nodes = cliques.map((c) => [...c]);
  const candidates: CliqueTreeEdge[] = [];
  for (let i = 0; i < nodes.length; i++) {
    for (let j = i + 1; j < nodes.length; j++) {
      const weight = intersectionSize(nodes[i], nodes[j]);
      if (weight > 0) candidates.push({ a: i, b: j, weight });
    }
  }
  candidates.sort((e1, e2) => e2.weight - e1.weight);

  const parent = nodes.map((_, i) => i);
  const find = (x: number): number => {
    while (parent[x] !== x) {
      parent[x] = parent[parent[x]];
      x = parent[x];
    }
    return x;
  };

  const edges: CliqueTreeEdge[] = [];
  for (const edge of candidates) {
    const ra = find(edge.a);
    const rb = find(edge.b);
    if (ra !== rb) {
      parent[ra] = rb;
      edges.push(edge);
      if (edges.length === nodes.length - 1) break;
    }
  }
  return { nodes, edges };
}

/** 校验运行交性质：每个图节点出现的团集合在树中连通。 */
export function satisfiesRunningIntersection(tree: CliqueTree): boolean {
  const occurrences = new Map<number, number[]>();
  tree.nodes.forEach((clique, idx) => {
    for (const v of clique) {
      if (!occurrences.has(v)) occurrences.set(v, []);
      occurrences.get(v)!.push(idx);
    }
  });

  const treeAdj = new Map<number, number[]>();
  for (const { a, b } of tree.edges) {
    if (!treeAdj.has(a)) treeAdj.set(a, []);
    if (!treeAdj.has(b)) treeAdj.set(b, []);
    treeAdj.get(a)!.push(b);
    treeAdj.get(b)!.push(a);
  }

  for (const idxList of occurrences.values()) {
    if (idxList.length <= 1) continue;
    // 从首个出现点 BFS，检查能否在“含该节点的团”子图内触达全部出现点
    const allowed = new Set(idxList);
    const visited = new Set<number>([idxList[0]]);
    const queue = [idxList[0]];
    while (queue.length > 0) {
      const cur = queue.pop()!;
      for (const nxt of treeAdj.get(cur) ?? []) {
        if (allowed.has(nxt) && !visited.has(nxt)) {
          visited.add(nxt);
          queue.push(nxt);
        }
      }
    }
    if (visited.size !== idxList.length) return false;
  }
  return true;
}

function intersectionSize(a: readonly number[], b: readonly number[]): number {
  const setB = new Set(b);
  let count = 0;
  for (const v of a) if (setB.has(v)) count++;
  return count;
}

export { cliqueKey };
