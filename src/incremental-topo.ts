/**
 * 双向增量拓扑排序与环检测器。
 *
 * 维护一个有向无环图（DAG）的动态拓扑序：
 *  - addEdge(u, v) 在 O(1) 快路径（前向边）下直接插入；
 *  - 后向边触发 Pearce–Kelly 双向局部搜索（前向 DFS + 反向 DFS），
 *    仅对受影响区域 [ord(v), ord(u)] 内的节点重新分配拓扑序号；
 *  - 若新边构成环，立即拒绝并返回完整因果环路节点列表，图与拓扑序保持不变。
 *
 * 由于 Node.js 单线程事件循环，同步的 addEdge 调用天然原子；
 * tryAddEdges 提供跨边界的全有或全无（all-or-nothing）批量提交语义。
 */

export type OkResult<T> = { ok: true; affected: T[] };
export type CycleResult<T> = { ok: false; cycle: T[] };
export type AddEdgeResult<T> = OkResult<T> | CycleResult<T>;

export type BatchResult<T> =
  | { ok: true; results: OkResult<T>[] }
  | { ok: false; cycle: T[]; failedIndex: number; failedEdge: [T, T] };

interface Snapshot<T> {
  succ: Map<T, Set<T>>;
  pred: Map<T, Set<T>>;
  ord: Map<T, number>;
  nextOrd: number;
}

export class IncrementalTopoSort<T = number> {
  private succ = new Map<T, Set<T>>();
  private pred = new Map<T, Set<T>>();
  private ord = new Map<T, number>();
  private nextOrd = 0;

  get size(): number {
    return this.ord.size;
  }

  addNode(v: T): void {
    if (!this.ord.has(v)) {
      this.ord.set(v, this.nextOrd++);
      this.succ.set(v, new Set());
      this.pred.set(v, new Set());
    }
  }

  hasNode(v: T): boolean {
    return this.ord.has(v);
  }

  hasEdge(u: T, v: T): boolean {
    return this.succ.get(u)?.has(v) ?? false;
  }

  /** 节点 v 当前的拓扑序号。 */
  rank(v: T): number | undefined {
    return this.ord.get(v);
  }

  /** 按当前拓扑序号升序返回全部节点。 */
  order(): T[] {
    return [...this.ord.entries()]
      .sort((a, b) => a[1] - b[1])
      .map(([v]) => v);
  }

  /**
   * 增加依赖边 u -> v。
   * 成环时不修改任何状态，返回完整环路 [v, ..., u, v]。
   */
  addEdge(u: T, v: T): AddEdgeResult<T> {
    this.addNode(u);
    this.addNode(v);

    if (u === v) {
      return { ok: false, cycle: [u, u] };
    }
    if (this.succ.get(u)!.has(v)) {
      return { ok: true, affected: [] }; // 幂等：重复边
    }

    const ordU = this.ord.get(u)!;
    const ordV = this.ord.get(v)!;

    // 快路径：前向边与现有拓扑序一致，不可能成环，无需重排。
    if (ordU < ordV) {
      this.link(u, v);
      return { ok: true, affected: [] };
    }

    // 后向边：在序号窗口 [ordV, ordU] 内做双向局部搜索。
    const lo = ordV;
    const hi = ordU;

    // 前向 DFS：从 v 出发，若可达 u 则新边 u->v 成环。
    const parent = new Map<T, T>();
    const seenF = new Set<T>([v]);
    const deltaF: T[] = [];
    const stack: T[] = [v];
    let cycle: T[] | null = null;

    while (stack.length > 0 && cycle === null) {
      const x = stack.pop()!;
      deltaF.push(x);
      for (const y of this.succ.get(x)!) {
        if (y === u) {
          cycle = this.buildCycle(parent, x, u, v);
          break;
        }
        const ordY = this.ord.get(y)!;
        if (ordY <= hi && !seenF.has(y)) {
          seenF.add(y);
          parent.set(y, x);
          stack.push(y);
        }
      }
    }

    if (cycle !== null) {
      return { ok: false, cycle }; // 拒绝：图与拓扑序均未改动
    }

    // 反向 DFS：从 u 沿前驱边收集 deltaB。
    const seenB = new Set<T>([u]);
    const deltaB: T[] = [];
    const queue: T[] = [u];
    while (queue.length > 0) {
      const x = queue.pop()!;
      deltaB.push(x);
      for (const y of this.pred.get(x)!) {
        const ordY = this.ord.get(y)!;
        if (ordY >= lo && !seenB.has(y)) {
          seenB.add(y);
          queue.push(y);
        }
      }
    }

    // deltaF 与 deltaB 必然不相交（否则 v ~> u 可达，早已判环）。
    // 重新染色：复用受影响节点原有的序号集合，deltaB 整体前移、deltaF 整体后移。
    const byOrd = (a: T, b: T) => this.ord.get(a)! - this.ord.get(b)!;
    deltaB.sort(byOrd);
    deltaF.sort(byOrd);
    const slots = [...deltaB, ...deltaF].map((x) => this.ord.get(x)!).sort((a, b) => a - b);
    const sequence = [...deltaB, ...deltaF];
    for (let i = 0; i < sequence.length; i++) {
      this.ord.set(sequence[i], slots[i]);
    }

    this.link(u, v);
    return { ok: true, affected: sequence };
  }

  /**
   * 批量加边，全有或全无：任一失败则整体回滚，返回失败边的因果环路。
   * 适用于把一组并发到达的依赖变更作为一次原子提交。
   */
  tryAddEdges(edges: Iterable<readonly [T, T]>): BatchResult<T> {
    const snap = this.snapshot();
    const results: OkResult<T>[] = [];
    let index = 0;
    for (const [u, v] of edges) {
      const r = this.addEdge(u, v);
      if (!r.ok) {
        this.restore(snap);
        return { ok: false, cycle: r.cycle, failedIndex: index, failedEdge: [u, v] };
      }
      results.push(r);
      index++;
    }
    return { ok: true, results };
  }

  /** 校验内部拓扑序与图结构一致（调试用）。 */
  validate(): boolean {
    const seen = new Set<number>();
    for (const o of this.ord.values()) {
      if (seen.has(o)) return false;
      seen.add(o);
    }
    for (const [u, outs] of this.succ) {
      for (const v of outs) {
        if (!(this.ord.get(u)! < this.ord.get(v)!)) return false;
      }
    }
    return true;
  }

  private link(u: T, v: T): void {
    this.succ.get(u)!.add(v);
    this.pred.get(v)!.add(u);
  }

  /** 由 parent 链重建环路：v ~> x -> u，再加上新边 u -> v 闭合。 */
  private buildCycle(parent: Map<T, T>, x: T, u: T, v: T): T[] {
    const path: T[] = [u, x];
    let cur = x;
    while (cur !== v) {
      cur = parent.get(cur)!;
      path.push(cur);
    }
    path.reverse(); // [v, ..., x, u]
    path.push(v); // 新边 u -> v 闭合环路
    return path;
  }

  private snapshot(): Snapshot<T> {
    const copySets = (m: Map<T, Set<T>>) =>
      new Map<T, Set<T>>([...m].map(([k, s]) => [k, new Set(s)]));
    return {
      succ: copySets(this.succ),
      pred: copySets(this.pred),
      ord: new Map(this.ord),
      nextOrd: this.nextOrd,
    };
  }

  private restore(snap: Snapshot<T>): void {
    this.succ = snap.succ;
    this.pred = snap.pred;
    this.ord = snap.ord;
    this.nextOrd = snap.nextOrd;
  }
}
