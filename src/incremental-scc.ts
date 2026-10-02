/**
 * 增量强连通分量（SCC）维护器。
 *
 * 支持动态有向图上的顶点增删与边插入/删除，并在每次变更后输出
 * 分量级差量（merge / split / vertex-added / vertex-removed）。
 *
 * 分量标识：分量内最小节点 ID（稳定，成员不变则 ID 不变）。
 *
 * 核心策略：
 *  - 边插入：仅当两端分属不同分量时，从终点做局部前向遍历、从起点做
 *    局部反向遍历，交集即为落在同一环上的顶点集合（增量缩点合并）。
 *  - 边删除 / 顶点删除：仅在受影响的分量内部诱导子图上做局部
 *    Tarjan 遍历，精确求出分裂后的新分量。
 */

export type VertexId = number;
export type ComponentId = number;

export interface SCCComponent {
  /** 分量标识 = 成员中的最小节点 ID */
  id: ComponentId;
  /** 升序排列的成员节点 */
  members: VertexId[];
}

export type SCCDiff =
  | { kind: 'vertex-added'; component: SCCComponent }
  | { kind: 'vertex-removed'; vertex: VertexId; component: SCCComponent }
  | { kind: 'merge'; component: SCCComponent; absorbed: ComponentId[] }
  | { kind: 'split'; from: ComponentId; components: SCCComponent[] };

export class IncrementalSCC {
  private adj = new Map<VertexId, Set<VertexId>>();
  private radj = new Map<VertexId, Set<VertexId>>();
  private compOf = new Map<VertexId, ComponentId>();
  private membersOf = new Map<ComponentId, Set<VertexId>>();

  // ---------------------------------------------------------------- 查询

  hasVertex(v: VertexId): boolean {
    return this.adj.has(v);
  }

  hasEdge(from: VertexId, to: VertexId): boolean {
    return this.adj.get(from)?.has(to) ?? false;
  }

  get vertexCount(): number {
    return this.adj.size;
  }

  /** 节点所属分量标识；节点不存在时返回 undefined */
  componentOf(v: VertexId): ComponentId | undefined {
    return this.compOf.get(v);
  }

  /** 指定分量的成员（升序）；分量不存在时返回 undefined */
  members(id: ComponentId): VertexId[] | undefined {
    const set = this.membersOf.get(id);
    return set ? [...set].sort((a, b) => a - b) : undefined;
  }

  /** 当前全部分量快照 */
  components(): SCCComponent[] {
    return [...this.membersOf.keys()]
      .sort((a, b) => a - b)
      .map((id) => ({ id, members: this.members(id)! }));
  }

  // ---------------------------------------------------------------- 顶点

  addVertex(v: VertexId): SCCDiff[] {
    if (this.adj.has(v)) {
      throw new Error(`vertex ${v} already exists`);
    }
    this.adj.set(v, new Set());
    this.radj.set(v, new Set());
    this.compOf.set(v, v);
    this.membersOf.set(v, new Set([v]));
    return [{ kind: 'vertex-added', component: { id: v, members: [v] } }];
  }

  removeVertex(v: VertexId): SCCDiff[] {
    const oldId = this.compOf.get(v);
    if (oldId === undefined) {
      throw new Error(`vertex ${v} does not exist`);
    }
    const oldMembers = this.membersOf.get(oldId)!;

    for (const to of this.adj.get(v)!) this.radj.get(to)!.delete(v);
    for (const from of this.radj.get(v)!) this.adj.get(from)!.delete(v);
    this.adj.delete(v);
    this.radj.delete(v);
    this.compOf.delete(v);
    oldMembers.delete(v);

    const diffs: SCCDiff[] = [
      { kind: 'vertex-removed', vertex: v, component: { id: oldId, members: [v] } },
    ];
    if (oldMembers.size === 0) {
      this.membersOf.delete(oldId);
    } else {
      // 剩余成员可能因该顶点的移除而分裂
      diffs.push(...this.resplit(oldId));
    }
    return diffs;
  }

  // ---------------------------------------------------------------- 边

  addEdge(from: VertexId, to: VertexId): SCCDiff[] {
    this.assertVertex(from);
    this.assertVertex(to);
    if (this.adj.get(from)!.has(to)) return [];

    this.adj.get(from)!.add(to);
    this.radj.get(to)!.add(from);

    const compFrom = this.compOf.get(from)!;
    const compTo = this.compOf.get(to)!;
    if (compFrom === compTo) return [];

    // 局部前向遍历：to 可达的全部顶点
    const forward = this.reachable(to, this.adj);
    if (!forward.has(from)) return []; // 无回边，缩点 DAG 仅多一条边

    // 局部反向遍历：可到达 from 的全部顶点
    const backward = this.reachable(from, this.radj);

    // 同时位于 to 的前向闭包与 from 的反向闭包中的顶点必在同一环上
    const merged: VertexId[] = [];
    for (const v of forward) {
      if (backward.has(v)) merged.push(v);
    }
    merged.sort((a, b) => a - b);

    const newId = merged[0];
    const absorbed: ComponentId[] = [];
    const newMembers = new Set<VertexId>(merged);
    for (const v of merged) {
      const oldId = this.compOf.get(v)!;
      if (oldId !== newId && this.membersOf.has(oldId)) {
        absorbed.push(oldId);
        this.membersOf.delete(oldId);
      }
      this.compOf.set(v, newId);
    }
    absorbed.sort((a, b) => a - b);
    this.membersOf.set(newId, newMembers);

    return [
      {
        kind: 'merge',
        component: { id: newId, members: merged },
        absorbed,
      },
    ];
  }

  removeEdge(from: VertexId, to: VertexId): SCCDiff[] {
    this.assertVertex(from);
    this.assertVertex(to);
    if (!this.adj.get(from)!.delete(to)) return [];
    this.radj.get(to)!.delete(from);

    if (this.compOf.get(from) !== this.compOf.get(to)) return [];
    // 两端同分量：该分量可能分裂，做局部重算
    return this.resplit(this.compOf.get(from)!);
  }

  // ---------------------------------------------------------------- 内部

  private assertVertex(v: VertexId): void {
    if (!this.adj.has(v)) throw new Error(`vertex ${v} does not exist`);
  }

  /** 沿给定邻接表从 start 做局部 DFS，返回可达顶点集合（含 start） */
  private reachable(
    start: VertexId,
    graph: Map<VertexId, Set<VertexId>>,
  ): Set<VertexId> {
    const seen = new Set<VertexId>([start]);
    const stack = [start];
    while (stack.length > 0) {
      const v = stack.pop()!;
      for (const next of graph.get(v)!) {
        if (!seen.has(next)) {
          seen.add(next);
          stack.push(next);
        }
      }
    }
    return seen;
  }

  /**
   * 对分量 id 的诱导子图做局部 Tarjan，精确重算 SCC。
   * 若确实分裂则更新内部状态并返回 split 差量，否则返回空数组。
   */
  private resplit(id: ComponentId): SCCDiff[] {
    const members = this.membersOf.get(id)!;
    const sccs = this.tarjan(members);
    if (sccs.length === 1) return [];

    this.membersOf.delete(id);
    const components: SCCComponent[] = [];
    for (const scc of sccs) {
      scc.sort((a, b) => a - b);
      const newId = scc[0];
      this.membersOf.set(newId, new Set(scc));
      for (const v of scc) this.compOf.set(v, newId);
      components.push({ id: newId, members: scc });
    }
    components.sort((a, b) => a.id - b.id);
    return [{ kind: 'split', from: id, components }];
  }

  /** 仅在 allowed 集合内的诱导子图上运行 Tarjan（递归，分量级规模） */
  private tarjan(allowed: Set<VertexId>): VertexId[][] {
    const index = new Map<VertexId, number>();
    const lowlink = new Map<VertexId, number>();
    const onStack = new Set<VertexId>();
    const stack: VertexId[] = [];
    const result: VertexId[][] = [];
    let counter = 0;

    const strongConnect = (v: VertexId): void => {
      index.set(v, counter);
      lowlink.set(v, counter);
      counter += 1;
      stack.push(v);
      onStack.add(v);

      for (const w of this.adj.get(v)!) {
        if (!allowed.has(w)) continue;
        if (!index.has(w)) {
          strongConnect(w);
          lowlink.set(v, Math.min(lowlink.get(v)!, lowlink.get(w)!));
        } else if (onStack.has(w)) {
          lowlink.set(v, Math.min(lowlink.get(v)!, index.get(w)!));
        }
      }

      if (lowlink.get(v) === index.get(v)) {
        const scc: VertexId[] = [];
        let w: VertexId;
        do {
          w = stack.pop()!;
          onStack.delete(w);
          scc.push(w);
        } while (w !== v);
        result.push(scc);
      }
    };

    for (const v of allowed) {
      if (!index.has(v)) strongConnect(v);
    }
    return result;
  }
}
