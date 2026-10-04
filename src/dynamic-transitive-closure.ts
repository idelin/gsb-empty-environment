/**
 * 动态传递闭包（Dynamic Transitive Closure）
 *
 * 支持有向边的批量增加 / 删除，维护所有可达对 (from, to) 及一条见证路径。
 *
 * 核心不变式：删除边时，仅当某可达对的见证路径真的经过了被删边，
 * 才尝试修复它——修复方式是在当前图上重新搜索一条替代路径，
 * 因此“由其他路径支撑的可达关系”绝不会被误删。
 */

export type Edge<N> = readonly [from: N, to: N];

/** 新增的可达关系，附带一条见证路径（节点序列，含起终点）。 */
export interface AddedReach<N> {
  from: N;
  to: N;
  path: N[];
}

/** 消失的可达关系，附带删除前最后一次记录的见证路径。 */
export interface RemovedReach<N> {
  from: N;
  to: N;
  previousPath: N[];
}

/** 一次批量更新产生的可达关系差量。 */
export interface Delta<N> {
  added: AddedReach<N>[];
  removed: RemovedReach<N>[];
}

export interface VerifyIssue<N> {
  kind: 'missing' | 'extra';
  from: N;
  to: N;
}

export interface VerifyResult<N> {
  ok: boolean;
  /** 全量搜索认为可达、但增量维护丢失的对 */
  missing: Array<{ from: N; to: N }>;
  /** 增量维护认为可达、但全量搜索认为不可达的对 */
  extra: Array<{ from: N; to: N }>;
}

const key2 = (a: number, b: number): string => `${a},${b}`;

export class DynamicTransitiveClosure<N> {
  /** 节点 -> 内部 id */
  private readonly idOf = new Map<N, number>();
  /** 内部 id -> 节点 */
  private readonly nodes: N[] = [];
  /** 邻接表（内部 id），边去重 */
  private readonly adj: Array<Set<number>> = [];
  /** from -> to -> 见证路径（内部 id 序列） */
  private readonly witness = new Map<number, Map<number, number[]>>();
  /** 反向索引：边 "a,b" -> 见证路径使用该边的可达对 "x,y" 集合 */
  private readonly edgeRefs = new Map<string, Set<string>>();

  // ---------------------------------------------------------------- 查询

  get nodeCount(): number {
    return this.nodes.length;
  }

  get edgeCount(): number {
    let n = 0;
    for (const outs of this.adj) n += outs.size;
    return n;
  }

  /** 当前维护的可达对数量 */
  get reachCount(): number {
    let n = 0;
    for (const m of this.witness.values()) n += m.size;
    return n;
  }

  hasEdge(u: N, v: N): boolean {
    const a = this.idOf.get(u);
    const b = this.idOf.get(v);
    return a !== undefined && b !== undefined && this.adj[a].has(b);
  }

  isReachable(u: N, v: N): boolean {
    const a = this.idOf.get(u);
    const b = this.idOf.get(v);
    if (a === undefined || b === undefined) return false;
    return this.witness.get(a)?.has(b) ?? false;
  }

  /** 返回 u 到 v 的一条见证路径（节点序列），不可达时返回 undefined。 */
  witnessPath(u: N, v: N): N[] | undefined {
    const a = this.idOf.get(u);
    const b = this.idOf.get(v);
    if (a === undefined || b === undefined) return undefined;
    const p = this.witness.get(a)?.get(b);
    return p ? p.map((id) => this.nodes[id]) : undefined;
  }

  /** 从 u 可达的所有节点。 */
  reachableFrom(u: N): N[] {
    const a = this.idOf.get(u);
    if (a === undefined) return [];
    const m = this.witness.get(a);
    if (!m) return [];
    return [...m.keys()].map((id) => this.nodes[id]);
  }

  /** 所有可达对及其见证路径。 */
  entries(): AddedReach<N>[] {
    const out: AddedReach<N>[] = [];
    for (const [x, m] of this.witness) {
      for (const [y, p] of m) {
        out.push({ from: this.nodes[x], to: this.nodes[y], path: p.map((id) => this.nodes[id]) });
      }
    }
    return out;
  }

  /** 当前所有边。 */
  edges(): Edge<N>[] {
    const out: Edge<N>[] = [];
    for (let a = 0; a < this.adj.length; a++) {
      for (const b of this.adj[a]) out.push([this.nodes[a], this.nodes[b]]);
    }
    return out;
  }

  // ---------------------------------------------------------------- 批量更新

  /**
   * 批量加边。重复边（已存在的边）自动去重，为空操作。
   * 返回本次新增的可达对差量（含见证路径）。
   */
  addEdges(edges: Iterable<Edge<N>>): Delta<N> {
    const delta: Delta<N> = { added: [], removed: [] };
    for (const [u, v] of edges) this.addEdgeSingle(u, v, delta);
    return delta;
  }

  /**
   * 批量删边。不存在的边为空操作。
   * 返回本次消失的可达对差量；被其他路径支撑的可达对会保留（不出现在差量中）。
   */
  removeEdges(edges: Iterable<Edge<N>>): Delta<N> {
    const delta: Delta<N> = { added: [], removed: [] };

    // 1. 真正删除存在的边（去重）
    const removedEdgeKeys: string[] = [];
    for (const [u, v] of edges) {
      const a = this.idOf.get(u);
      const b = this.idOf.get(v);
      if (a === undefined || b === undefined || !this.adj[a].has(b)) continue;
      this.adj[a].delete(b);
      removedEdgeKeys.push(key2(a, b));
    }
    if (removedEdgeKeys.length === 0) return delta;

    // 2. 找出见证路径经过被删边的可达对（只修复这些，其余一律不动）
    const affected = new Set<string>();
    for (const ek of removedEdgeKeys) {
      const refs = this.edgeRefs.get(ek);
      if (refs) for (const pk of refs) affected.add(pk);
    }

    // 3. 逐对修复：在当前图上重搜替代路径；找不到才真正移除
    for (const pk of affected) {
      const sep = pk.indexOf(',');
      const x = Number(pk.slice(0, sep));
      const y = Number(pk.slice(sep + 1));
      const old = this.witness.get(x)?.get(y);
      if (!old) continue;
      if (this.pathValid(old)) continue; // 见证路径未受影响（理论上不会发生，防御性检查）

      const alt = this.bfsPath(x, y);
      if (alt) {
        this.registerWitness(x, y, alt); // 换一条见证路径，可达关系保留
      } else {
        this.unregisterWitness(x, y);
        delta.removed.push({
          from: this.nodes[x],
          to: this.nodes[y],
          previousPath: old.map((id) => this.nodes[id]),
        });
      }
    }

    // 4. 清理被删边的反向索引
    for (const ek of removedEdgeKeys) this.edgeRefs.delete(ek);
    return delta;
  }

  /**
   * 一次调用内先删后加，返回合并后的差量。
   * 注意：差量按实际发生顺序拼接，同一对可能先出现在 removed 又出现在 added。
   */
  update(batch: { add?: Iterable<Edge<N>>; remove?: Iterable<Edge<N>> }): Delta<N> {
    const d1 = batch.remove ? this.removeEdges(batch.remove) : { added: [], removed: [] };
    const d2 = batch.add ? this.addEdges(batch.add) : { added: [], removed: [] };
    return { added: [...d1.added, ...d2.added], removed: [...d1.removed, ...d2.removed] };
  }

  // ---------------------------------------------------------------- 对照验证

  /**
   * 全量重算：从每个节点 BFS，返回完整的可达对及路径（作为对照基准）。
   */
  recompute(): AddedReach<N>[] {
    const out: AddedReach<N>[] = [];
    for (let s = 0; s < this.nodes.length; s++) {
      const parent = new Map<number, number>();
      const visited = new Set<number>();
      const queue: number[] = [s];
      // 不预先标记 s：这样经环回到 s 时能记录 (s, s)
      while (queue.length > 0) {
        const cur = queue.shift()!;
        for (const nb of this.adj[cur]) {
          if (visited.has(nb)) continue;
          visited.add(nb);
          parent.set(nb, cur);
          queue.push(nb);
        }
      }
      for (const t of visited) {
        const ids = t === s
          ? [...this.buildPath(parent, s, parent.get(t)!), s] // 经环回到自身
          : this.buildPath(parent, s, t);
        out.push({ from: this.nodes[s], to: this.nodes[t], path: ids.map((id) => this.nodes[id]) });
      }
    }
    return out;
  }

  /**
   * 将增量维护的结果与全量重算对照，返回差异（ok 为 true 表示完全一致）。
   */
  verify(): VerifyResult<N> {
    const maintained = new Set<string>();
    for (const [x, m] of this.witness) for (const y of m.keys()) maintained.add(key2(x, y));

    const full = new Set<string>();
    for (let s = 0; s < this.nodes.length; s++) {
      const visited = new Set<number>();
      const queue: number[] = [s];
      while (queue.length > 0) {
        const cur = queue.shift()!;
        for (const nb of this.adj[cur]) {
          if (visited.has(nb)) continue;
          visited.add(nb);
          queue.push(nb);
          full.add(key2(s, nb));
        }
      }
    }

    const missing: Array<{ from: N; to: N }> = [];
    const extra: Array<{ from: N; to: N }> = [];
    for (const pk of full) {
      if (!maintained.has(pk)) {
        const sep = pk.indexOf(',');
        missing.push({ from: this.nodes[Number(pk.slice(0, sep))], to: this.nodes[Number(pk.slice(sep + 1))] });
      }
    }
    for (const pk of maintained) {
      if (!full.has(pk)) {
        const sep = pk.indexOf(',');
        extra.push({ from: this.nodes[Number(pk.slice(0, sep))], to: this.nodes[Number(pk.slice(sep + 1))] });
      }
    }
    return { ok: missing.length === 0 && extra.length === 0, missing, extra };
  }

  // ---------------------------------------------------------------- 内部实现

  private intern(n: N): number {
    let id = this.idOf.get(n);
    if (id === undefined) {
      id = this.nodes.length;
      this.nodes.push(n);
      this.idOf.set(n, id);
      this.adj.push(new Set());
    }
    return id;
  }

  private addEdgeSingle(u: N, v: N, delta: Delta<N>): void {
    const a = this.intern(u);
    const b = this.intern(v);
    if (this.adj[a].has(b)) return; // 重复边：幂等空操作
    this.adj[a].add(b);

    // 新可达对 = (能到 a 的点 ∪ {a}) × (b 能到的点 ∪ {b})，均取加边前的快照。
    // 新对必然经过新边 a->b，因此用旧快照做笛卡尔积一趟即可，环也不会遗漏。
    const preds: number[] = [a];
    for (const [x, m] of this.witness) if (m.has(a)) preds.push(x);
    const succs: number[] = [b];
    const fromB = this.witness.get(b);
    if (fromB) for (const y of fromB.keys()) succs.push(y);

    for (const x of preds) {
      const p1 = x === a ? [a] : this.witness.get(x)!.get(a)!;
      for (const y of succs) {
        if (this.witness.get(x)?.has(y)) continue; // 已可达，保留原见证
        const p2 = y === b ? [b] : this.witness.get(b)!.get(y)!;
        const path = [...p1, ...p2]; // p1 终点为 a，p2 起点为 b，中间即新边 a->b
        this.registerWitness(x, y, path);
        delta.added.push({ from: this.nodes[x], to: this.nodes[y], path: path.map((id) => this.nodes[id]) });
      }
    }
  }

  /**
   * 在当前邻接表上 BFS 搜索 x -> y 的一条路径（内部 id 序列），找不到返回 undefined。
   * x === y 时寻找一条经过 x 的环（长度 >= 1）。
   */
  private bfsPath(x: number, y: number): number[] | undefined {
    const parent = new Map<number, number>();
    const visited = new Set<number>();
    if (x !== y) visited.add(x); // x === y 时不标记起点，允许经环回到 x
    const queue: number[] = [x];
    while (queue.length > 0) {
      const cur = queue.shift()!;
      for (const nb of this.adj[cur]) {
        if (nb === y) return [...this.buildPath(parent, x, cur), nb];
        if (visited.has(nb)) continue;
        visited.add(nb);
        parent.set(nb, cur);
        queue.push(nb);
      }
    }
    return undefined;
  }

  private buildPath(parent: Map<number, number>, s: number, t: number): number[] {
    const path: number[] = [t];
    let cur = t;
    while (cur !== s) {
      cur = parent.get(cur)!;
      path.push(cur);
    }
    return path.reverse();
  }

  /** 检查见证路径的每条边是否仍存在于当前图中。 */
  private pathValid(path: number[]): boolean {
    for (let i = 0; i + 1 < path.length; i++) {
      if (!this.adj[path[i]].has(path[i + 1])) return false;
    }
    return true;
  }

  /** 登记/替换 (x,y) 的见证路径，并维护 边->可达对 反向索引。 */
  private registerWitness(x: number, y: number, path: number[]): void {
    this.unregisterWitness(x, y);
    let m = this.witness.get(x);
    if (!m) {
      m = new Map();
      this.witness.set(x, m);
    }
    m.set(y, path);
    const pk = key2(x, y);
    for (let i = 0; i + 1 < path.length; i++) {
      const ek = key2(path[i], path[i + 1]);
      let refs = this.edgeRefs.get(ek);
      if (!refs) {
        refs = new Set();
        this.edgeRefs.set(ek, refs);
      }
      refs.add(pk);
    }
  }

  private unregisterWitness(x: number, y: number): void {
    const m = this.witness.get(x);
    const old = m?.get(y);
    if (!m || !old) return;
    m.delete(y);
    if (m.size === 0) this.witness.delete(x);
    const pk = key2(x, y);
    for (let i = 0; i + 1 < old.length; i++) {
      const refs = this.edgeRefs.get(key2(old[i], old[i + 1]));
      if (refs) {
        refs.delete(pk);
        if (refs.size === 0) this.edgeRefs.delete(key2(old[i], old[i + 1]));
      }
    }
  }
}
