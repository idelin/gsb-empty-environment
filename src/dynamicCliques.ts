import { Graph } from './graph.ts';
import { maximalCliques, cliqueKey } from './bronKerbosch.ts';

/** 一次边变更引起的极大团差异。 */
export interface CliqueDelta {
  /** 新诞生的极大团（合并产物或全新团）。 */
  born: number[][];
  /** 被消解的极大团（被新团包含吸收，或因删边退化消失）。 */
  dissolved: number[][];
}

/**
 * 动态极大团追踪器。
 *
 * 增删边后重新枚举受影响图的极大团集合并与旧集合做差异比对：
 * - 新边使相邻团合并为更大的团时，旧团进入 dissolved，合并团进入 born；
 * - 删边使团退化分裂时，原团进入 dissolved，分裂出的新极大团进入 born。
 */
export class DynamicCliqueTracker {
  readonly graph = new Graph();
  private cliques: number[][] = [];
  private index = new Map<string, number[]>();

  constructor(edges: ReadonlyArray<readonly [number, number]> = []) {
    for (const [u, v] of edges) this.graph.addEdge(u, v);
    this.recompute();
  }

  private recompute(): void {
    this.cliques = maximalCliques(this.graph);
    this.index = new Map(this.cliques.map((c) => [cliqueKey(c), c]));
  }

  private mutate(fn: () => void): CliqueDelta {
    const before = this.index;
    fn();
    this.recompute();
    const born: number[][] = [];
    const dissolved: number[][] = [];
    for (const [key, clique] of this.index) {
      if (!before.has(key)) born.push(clique);
    }
    for (const [key, clique] of before) {
      if (!this.index.has(key)) dissolved.push(clique);
    }
    return { born, dissolved };
  }

  addEdge(u: number, v: number): CliqueDelta {
    return this.mutate(() => this.graph.addEdge(u, v));
  }

  removeEdge(u: number, v: number): CliqueDelta {
    return this.mutate(() => this.graph.removeEdge(u, v));
  }

  /** 当前全部极大团（节点升序）。 */
  getMaximalCliques(): number[][] {
    return this.cliques.map((c) => [...c]);
  }

  /** 包含指定节点集合的全部极大团。 */
  cliquesContaining(nodes: readonly number[]): number[][] {
    const want = [...nodes];
    return this.cliques.filter((c) => {
      const members = new Set(c);
      return want.every((n) => members.has(n));
    }).map((c) => [...c]);
  }
}
