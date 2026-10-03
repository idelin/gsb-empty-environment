import { Graph } from './graph.ts';
import { listMaximalCliques } from './bronKerbosch.ts';

export interface CliqueChange {
  /** 本次更新后新诞生的极大团。 */
  born: number[][];
  /** 本次更新后消解（不再极大/不再是团）的极大团。 */
  dissolved: number[][];
}

const keyOf = (clique: number[]): string => clique.join(',');

const sorted = (clique: Iterable<number>): number[] =>
  [...clique].sort((a, b) => a - b);

const isSubsetOf = (a: number[], b: number[]): boolean => {
  const setB = new Set(b);
  return a.every((x) => setB.has(x));
};

/**
 * 动态图极大团追踪器。
 *
 * 内部维护一份“当前全部极大团”的索引，边增删时做增量更新而非全量重算：
 *
 * 加边 (u,v)：
 *  - 新诞生的极大团必同时含 u,v；而含 u,v 的团 K 极大 ⟺ K\{u,v}
 *    是公共邻域 G[N(u)∩N(v)] 中的极大团，故只需在公共邻域上跑
 *    Bron–Kerbosch 即可找齐新团。
 *  - 旧极大团 C 若只含 u,v 之一（设为 u），且 v 与 C 中所有顶点相邻，
 *    则 C 被 C∪{v} 吸收，从索引中剔除。
 *
 * 删边 (u,v)：
 *  - 所有同时含 u,v 的旧极大团 C 失效；其退化候选 C\{u}、C\{v} 若不被
 *    其他现存极大团包含，则成为新的极大团（可证明删边后新诞生的极大团
 *    必为此类候选之一）。
 */
export class DynamicCliqueTracker {
  readonly graph = new Graph();
  /** 规范化（升序）团 -> 原始团 的索引。 */
  private cliques: Map<string, number[]> = new Map();

  constructor(edges: [number, number][] = []) {
    for (const [u, v] of edges) this.graph.addEdge(u, v);
    for (const c of listMaximalCliques(this.graph)) {
      this.cliques.set(keyOf(c), c);
    }
  }

  /** 当前全部极大团（每个内部升序）。 */
  maximalCliques(): number[][] {
    return [...this.cliques.values()].map((c) => [...c]);
  }

  /** 包含指定节点集合的全部极大团。 */
  maximalCliquesContaining(nodes: Iterable<number>): number[][] {
    const want = [...nodes];
    return this.maximalCliques().filter((c) => isSubsetOf(want, c));
  }

  /** 动态加边，返回极大团的诞生/消解变化。 */
  addEdge(u: number, v: number): CliqueChange {
    if (u === v) throw new Error(`自环非法: ${u}`);
    if (this.graph.hasEdge(u, v)) return { born: [], dissolved: [] };
    this.graph.addEdge(u, v);

    // 1. 公共邻域上的极大团 + {u,v} = 全部新诞生的极大团
    const common = new Set<number>();
    for (const w of this.graph.neighbors(u)) {
      if (this.graph.hasEdge(w, v)) common.add(w);
    }
    // 公共邻域为空时约定其“极大团”为空集，即新团就是 {u,v} 本身
    const bases = common.size === 0
      ? [[]]
      : listMaximalCliques(this.graph, common);
    const born = bases.map((c) => sorted([u, v, ...c]));

    // 2. 剔除被吸收的旧极大团（只含一个端点且另一端点与其全相邻）
    const dissolved: number[][] = [];
    for (const [key, c] of this.cliques) {
      const hasU = c.includes(u);
      const hasV = c.includes(v);
      if (hasU === hasV) continue; // 同时含或同时不含均不受影响
      const other = hasU ? v : u;
      if (c.every((w) => this.graph.hasEdge(w, other))) {
        this.cliques.delete(key);
        dissolved.push(c);
      }
    }

    for (const c of born) this.cliques.set(keyOf(c), c);
    return { born, dissolved };
  }

  /** 动态删边，返回极大团的诞生/消解变化。 */
  removeEdge(u: number, v: number): CliqueChange {
    if (!this.graph.hasEdge(u, v)) return { born: [], dissolved: [] };
    this.graph.removeEdge(u, v);

    const dissolved: number[][] = [];
    const candidates: number[][] = [];

    // 1. 同时含 u,v 的旧极大团失效，产生退化候选 C\{u}、C\{v}
    for (const [key, c] of this.cliques) {
      if (c.includes(u) && c.includes(v)) {
        this.cliques.delete(key);
        dissolved.push(c);
        candidates.push(sorted(c.filter((w) => w !== u)));
        candidates.push(sorted(c.filter((w) => w !== v)));
      }
    }

    // 2. 候选去重后，若不被现存极大团（或其他更大的候选）包含，则诞生
    const uniq = new Map<string, number[]>();
    for (const c of candidates) {
      const k = keyOf(c);
      if (!uniq.has(k)) uniq.set(k, c);
    }
    const pool = [...uniq.values()];
    const born: number[][] = [];
    for (const cand of pool) {
      const key = keyOf(cand);
      if (this.cliques.has(key)) continue;
      const covered =
        [...this.cliques.values()].some((c) => isSubsetOf(cand, c)) ||
        pool.some(
          (other) => other !== cand && other.length >= cand.length &&
            isSubsetOf(cand, other),
        );
      if (!covered) {
        this.cliques.set(key, cand);
        born.push(cand);
      }
    }
    return { born, dissolved };
  }
}
