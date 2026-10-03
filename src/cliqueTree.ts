import { Graph } from './graph.ts';
import { listMaximalCliques } from './bronKerbosch.ts';

export interface CliqueTreeNode {
  id: number;
  /** 袋（bag）：一个极大团。 */
  bag: number[];
}

export interface CliqueTreeEdge {
  from: number;
  to: number;
  /** 分隔集 = 两个袋的交集。 */
  separator: number[];
}

export interface CliqueTree {
  chordal: boolean;
  nodes: CliqueTreeNode[];
  edges: CliqueTreeEdge[];
}

/**
 * 最大势搜索（Maximum Cardinality Search）判断弦性，
 * 并在弦图上返回完美消元序的逆序。
 */
export function maximumCardinalitySearch(graph: Graph): {
  chordal: boolean;
  order: number[];
} {
  const vertices = graph.vertices();
  const weight = new Map<number, number>(vertices.map((v) => [v, 0]));
  const unnumbered = new Set(vertices);
  const order: number[] = [];

  while (unnumbered.size > 0) {
    let best = -1;
    let bestW = -1;
    for (const v of unnumbered) {
      const w = weight.get(v)!;
      if (w > bestW) {
        bestW = w;
        best = v;
      }
    }
    unnumbered.delete(best);
    order.push(best);
    for (const u of graph.neighbors(best)) {
      if (unnumbered.has(u)) weight.set(u, weight.get(u)! + 1);
    }
  }

  // 验证 order 的逆序是否为完美消元序：
  // 对每个顶点 v，其在 order 中先于 v 的邻居必须两两相邻。
  const pos = new Map(order.map((v, i) => [v, i]));
  for (const v of vertices) {
    const earlier = [...graph.neighbors(v)].filter(
      (u) => pos.get(u)! < pos.get(v)!,
    );
    for (let i = 0; i < earlier.length; i++) {
      for (let j = i + 1; j < earlier.length; j++) {
        if (!graph.hasEdge(earlier[i], earlier[j])) {
          return { chordal: false, order };
        }
      }
    }
  }
  return { chordal: true, order };
}

/**
 * 团树（联结树）分解：
 *  - 图必须是弦图，否则返回 { chordal: false, ... }；
 *  - 以全部极大团为袋，团交图的最大生成树满足运行交性质
 *    （对任意顶点 v，含 v 的袋构成连通子树）。
 */
export function buildCliqueTree(
  graph: Graph,
  maximalCliques?: number[][],
): CliqueTree {
  const { chordal } = maximumCardinalitySearch(graph);
  if (!chordal) return { chordal: false, nodes: [], edges: [] };

  const bags = (maximalCliques ?? listMaximalCliques(graph)).map((c) =>
    [...c].sort((a, b) => a - b),
  );
  const nodes: CliqueTreeNode[] = bags.map((bag, id) => ({ id, bag }));
  if (nodes.length <= 1) return { chordal: true, nodes, edges: [] };

  // Prim 最大生成树，边权 = 袋交集大小
  const inTree = new Set<number>([0]);
  const edges: CliqueTreeEdge[] = [];
  while (inTree.size < nodes.length) {
    let bestFrom = -1;
    let bestTo = -1;
    let bestW = -1;
    for (const i of inTree) {
      const setI = new Set(bags[i]);
      for (let j = 0; j < nodes.length; j++) {
        if (inTree.has(j)) continue;
        const w = bags[j].filter((x) => setI.has(x)).length;
        if (w > bestW) {
          bestW = w;
          bestFrom = i;
          bestTo = j;
        }
      }
    }
    inTree.add(bestTo);
    const setFrom = new Set(bags[bestFrom]);
    edges.push({
      from: bestFrom,
      to: bestTo,
      separator: bags[bestTo].filter((x) => setFrom.has(x)),
    });
  }
  return { chordal: true, nodes, edges };
}
