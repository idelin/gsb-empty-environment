/**
 * 双向增量拓扑排序与环检测器
 *
 * 基于 Pearce & Kelly 增量拓扑排序算法：
 *  - 每个节点维护一个拓扑序号 label，保证任意边 x->y 满足 label[x] < label[y]
 *  - 加边时先做可达性检查，成环则立即拒绝并输出完整因果环路
 *  - 未成环且序号违逆时，在区间 [lb, ub] 内做前向 DFS + 反向 DFS
 *    双向局部搜索（重新染色），仅重排受影响节点的序号
 *  - 通过 Promise 链串行化，支持并发调用 addEdge
 */

export class CycleError extends Error {
  readonly cycle: unknown[];

  constructor(cycle: unknown[]) {
    super(`dependency cycle detected: ${cycle.join(' -> ')}`);
    this.name = 'CycleError';
    this.cycle = cycle;
  }
}

export class IncrementalTopo {
  private label = new Map<unknown, number>();
  private adj = new Map<unknown, Set<unknown>>();   // 正向边 x -> y
  private radj = new Map<unknown, Set<unknown>>();  // 反向边 y <- x
  private nextLabel = 0;
  private queue: Promise<void> = Promise.resolve();

  /** 注册节点（幂等）。 */
  addNode(v: unknown): void {
    if (!this.label.has(v)) {
      this.label.set(v, this.nextLabel++);
      this.adj.set(v, new Set());
      this.radj.set(v, new Set());
    }
  }

  /**
   * 并发安全地增加依赖边 x -> y。
   * 成环时 reject CycleError（cycle 为完整因果环路，如 [2, 3, 4, 2]），
   * 且图与拓扑序保持不变。
   */
  addEdge(x: unknown, y: unknown): Promise<void> {
    const run = this.queue.then(() => this.insertEdge(x, y));
    // 失败不阻塞后续加边
    this.queue = run.catch(() => {});
    return run;
  }

  /** 同步版本（内部串行，适合非并发场景）。 */
  addEdgeSync(x: unknown, y: unknown): void {
    this.insertEdge(x, y);
  }

  /** 当前拓扑序（按序号升序的节点列表）。 */
  getOrder(): unknown[] {
    return [...this.label.entries()]
      .sort((a, b) => a[1] - b[1])
      .map(([v]) => v);
  }

  /** 节点当前拓扑序号。 */
  indexOf(v: unknown): number {
    const l = this.label.get(v);
    if (l === undefined) throw new Error(`unknown node: ${String(v)}`);
    return l;
  }

  /** 校验当前序号满足所有边的拓扑单调性（调试用）。 */
  isValid(): boolean {
    for (const [x, ys] of this.adj) {
      for (const y of ys) {
        if (this.label.get(x)! >= this.label.get(y)!) return false;
      }
    }
    return true;
  }

  private insertEdge(x: unknown, y: unknown): void {
    this.addNode(x);
    this.addNode(y);
    if (this.adj.get(x)!.has(y)) return; // 重复边，幂等

    // 环检测：若 y 已可达 x，则 x->y 成环，拒绝且不修改任何状态
    const path = this.findPath(y, x);
    if (path !== null) {
      throw new CycleError([...path, y]);
    }

    this.adj.get(x)!.add(y);
    this.radj.get(y)!.add(x);

    // 序号违逆时才需要双向局部重排
    if (this.label.get(x)! > this.label.get(y)!) {
      this.reorder(x, y);
    }
  }

  /** BFS 求 from 到 to 的一条路径（含端点），不可达返回 null。 */
  private findPath(from: unknown, to: unknown): unknown[] | null {
    if (from === to) return [from];
    const prev = new Map<unknown, unknown>();
    const visited = new Set<unknown>([from]);
    const queue: unknown[] = [from];
    while (queue.length > 0) {
      const u = queue.shift()!;
      for (const w of this.adj.get(u)!) {
        if (visited.has(w)) continue;
        visited.add(w);
        prev.set(w, u);
        if (w === to) {
          const path: unknown[] = [to];
          let cur = to;
          while (cur !== from) {
            cur = prev.get(cur)!;
            path.unshift(cur);
          }
          return path;
        }
        queue.push(w);
      }
    }
    return null;
  }

  /**
   * Pearce & Kelly 双向局部重排：
   *  - deltaF：从 y 前向 DFS，仅访问 label < label[x] 的节点（上界 ub）
   *  - deltaB：从 x 反向 DFS，仅访问 label > label[y] 的节点（下界 lb）
   *  - 合并后按旧序号排序，重新分配原有序号槽位，区间外节点不受影响
   */
  private reorder(x: unknown, y: unknown): void {
    const ub = this.label.get(x)!;
    const lb = this.label.get(y)!;

    const deltaF = new Set<unknown>();
    const stackF: unknown[] = [y];
    while (stackF.length > 0) {
      const u = stackF.pop()!;
      if (deltaF.has(u)) continue;
      deltaF.add(u);
      for (const w of this.adj.get(u)!) {
        if (!deltaF.has(w) && this.label.get(w)! < ub) stackF.push(w);
      }
    }

    const deltaB = new Set<unknown>();
    const stackB: unknown[] = [x];
    while (stackB.length > 0) {
      const u = stackB.pop()!;
      if (deltaB.has(u)) continue;
      deltaB.add(u);
      for (const w of this.radj.get(u)!) {
        if (!deltaB.has(w) && this.label.get(w)! > lb) stackB.push(w);
      }
    }

    // PK 算法关键：deltaB（x 侧）整体排在 deltaF（y 侧）之前，
    // 组内按旧序号排序；序号槽位取两组旧序号的排序合集
    const byLabel = (a: unknown, b: unknown) =>
      this.label.get(a)! - this.label.get(b)!;
    const affected = [...[...deltaB].sort(byLabel), ...[...deltaF].sort(byLabel)];
    const slots = affected.map((v) => this.label.get(v)!).sort((a, b) => a - b);
    for (let i = 0; i < affected.length; i++) {
      this.label.set(affected[i], slots[i]);
    }
  }
}
