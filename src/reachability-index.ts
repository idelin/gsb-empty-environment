import { CompressedBitmap } from "./compressed-bitmap.ts";

export class CycleError extends Error {
  constructor(from: string, to: string) {
    super(`edge ${from} -> ${to} would create a cycle`);
    this.name = "CycleError";
  }
}

export interface IndexStats {
  nodes: number;
  edges: number;
  /** 压缩位图实际占用（估算，字节） */
  bitmapBytes: number;
  /** 稠密 n x n 邻接矩阵占用（字节） */
  denseBytes: number;
  /** denseBytes / bitmapBytes */
  compressionRatio: number;
}

/**
 * 有向无环图（DAG）全节点可达性位图索引器。
 *
 * - 构建：Kahn 拓扑排序 + 最长路径分层；按 (层级, 节点 id) 分配位位置，
 *   使同一节点的下游可达位在位图上聚集成连续段，利于稀疏块压缩。
 * - 查询：reach[u] 的第 pos[v] 位即 u→v 可达性，单次 O(1)。
 * - 边插入：沿反向边做 delta 传播，只把“新增可达位”用块级位运算
 *   并行合并进所有祖先的可达集，无需重建全图闭包。
 */
export class DAGReachabilityIndex {
  private readonly labels: string[] = [];
  private readonly idOf = new Map<string, number>();
  private readonly children: number[][] = [];
  private readonly parents: number[][] = [];
  private reach: CompressedBitmap[] = [];
  private position: number[] = [];
  private labelAtPosition: string[] = [];
  private levels: number[] = [];
  private edgeTotal = 0;
  private built = false;
  private levelsDirty = true;

  addNode(label?: string): number {
    const name = label ?? `n${this.labels.length}`;
    const existing = this.idOf.get(name);
    if (existing !== undefined) return existing;
    const id = this.labels.length;
    this.labels.push(name);
    this.idOf.set(name, id);
    this.children.push([]);
    this.parents.push([]);
    this.built = false;
    this.levelsDirty = true;
    return id;
  }

  hasNode(label: string): boolean {
    return this.idOf.has(label);
  }

  get nodeCount(): number {
    return this.labels.length;
  }

  get edgeCount(): number {
    return this.edgeTotal;
  }

  addEdge(from: string, to: string): void {
    if (from === to) throw new CycleError(from, to);
    const u = this.addNode(from);
    const v = this.addNode(to);
    if (this.children[u].includes(v)) return;

    if (this.built) {
      // v 已可达 u 则成环；位图查询 O(1)
      if (this.reach[v].test(this.position[u])) throw new CycleError(from, to);
    } else if (this.dfsReachable(v, u)) {
      throw new CycleError(from, to);
    }

    this.children[u].push(v);
    this.parents[v].push(u);
    this.edgeTotal += 1;
    this.levelsDirty = true;

    if (this.built) this.propagate(u, v);
  }

  /** 全量构建：拓扑分层 + 逆拓扑序位并行求传递闭包 */
  build(): void {
    const order = this.topoSort();
    this.computeLevels(order);

    const byLayer = [...order].sort(
      (a, b) => this.levels[a] - this.levels[b] || a - b,
    );
    this.position = new Array<number>(byLayer.length);
    this.labelAtPosition = new Array<string>(byLayer.length);
    byLayer.forEach((id, rank) => {
      this.position[id] = rank;
      this.labelAtPosition[rank] = this.labels[id];
    });

    this.reach = new Array<CompressedBitmap>(this.labels.length);
    for (let i = order.length - 1; i >= 0; i -= 1) {
      const v = order[i];
      const bitmap = new CompressedBitmap();
      bitmap.set(this.position[v]);
      for (const w of this.children[v]) bitmap.orInPlace(this.reach[w]);
      this.reach[v] = bitmap;
    }
    this.built = true;
  }

  /** O(1) 可达性查询：u 是否可达 v */
  isReachable(from: string, to: string): boolean {
    this.ensureBuilt();
    const u = this.idOf.get(from);
    const v = this.idOf.get(to);
    if (u === undefined || v === undefined) return false;
    return this.isReachableById(u, v);
  }

  /** 标签 -> 内部 id，便于批量查询时绕开字符串哈希 */
  nodeId(label: string): number | undefined {
    return this.idOf.get(label);
  }

  /** O(1) 可达性查询的 id 版本：一次位图位测试 */
  isReachableById(u: number, v: number): boolean {
    this.ensureBuilt();
    return this.reach[u].test(this.position[v]);
  }

  /** u 的全部可达节点（含自身），按拓扑层级顺序返回 */
  reachableFrom(label: string): string[] {
    this.ensureBuilt();
    const u = this.idOf.get(label);
    if (u === undefined) return [];
    return [...this.reach[u].indices()].map((pos) => this.labelAtPosition[pos]);
  }

  levelOf(label: string): number {
    this.ensureLevels();
    const id = this.idOf.get(label);
    if (id === undefined) throw new Error(`unknown node: ${label}`);
    return this.levels[id];
  }

  /** 层级 -> 节点标签列表，便于检视分层结果 */
  layersSnapshot(): string[][] {
    this.ensureLevels();
    const layers: string[][] = [];
    for (let id = 0; id < this.labels.length; id += 1) {
      const level = this.levels[id];
      (layers[level] ??= []).push(this.labels[id]);
    }
    return layers;
  }

  stats(): IndexStats {
    this.ensureBuilt();
    const n = this.labels.length;
    const bitmapBytes = this.reach.reduce((sum, b) => sum + b.byteSize(), 0);
    const denseBytes = Math.ceil(n / 8) * n;
    return {
      nodes: n,
      edges: this.edgeTotal,
      bitmapBytes,
      denseBytes,
      compressionRatio: denseBytes / Math.max(bitmapBytes, 1),
    };
  }

  /**
   * 增量更新：插入 u->v 后，u 的全部祖先（含 u）需要并入
   * reach[v] ∪ {v}。沿反向边做 BFS，仅传播差集（delta），
   * 每个节点的合并是一次 64 位块级 OR，位并行完成。
   */
  private propagate(u: number, v: number): void {
    const seed = this.reach[v].clone();
    seed.set(this.position[v]);
    const pending = new Map<number, CompressedBitmap>();
    pending.set(u, seed);

    while (pending.size > 0) {
      const next = pending.entries().next();
      if (next.done) break;
      const [x, delta] = next.value;
      pending.delete(x);

      const fresh = delta.subtract(this.reach[x]);
      if (fresh.isEmpty) continue;
      this.reach[x].orInPlace(fresh);

      for (const p of this.parents[x]) {
        const queued = pending.get(p);
        if (queued) queued.orInPlace(fresh);
        else pending.set(p, fresh.clone());
      }
    }
  }

  private topoSort(): number[] {
    const indegree = this.parents.map((ps) => ps.length);
    const queue: number[] = [];
    for (let id = 0; id < indegree.length; id += 1) {
      if (indegree[id] === 0) queue.push(id);
    }
    const order: number[] = [];
    for (let head = 0; head < queue.length; head += 1) {
      const v = queue[head];
      order.push(v);
      for (const w of this.children[v]) {
        indegree[w] -= 1;
        if (indegree[w] === 0) queue.push(w);
      }
    }
    if (order.length !== this.labels.length) {
      throw new CycleError("<graph>", "<graph>");
    }
    return order;
  }

  /** 最长路径分层：level[v] = max(level[p]) + 1，源节点为 0 */
  private computeLevels(order: number[]): void {
    this.levels = new Array<number>(this.labels.length).fill(0);
    for (const v of order) {
      for (const w of this.children[v]) {
        if (this.levels[w] < this.levels[v] + 1) {
          this.levels[w] = this.levels[v] + 1;
        }
      }
    }
    this.levelsDirty = false;
  }

  private dfsReachable(from: number, to: number): boolean {
    const seen = new Set<number>([from]);
    const stack = [from];
    while (stack.length > 0) {
      const v = stack.pop() as number;
      if (v === to) return true;
      for (const w of this.children[v]) {
        if (!seen.has(w)) {
          seen.add(w);
          stack.push(w);
        }
      }
    }
    return false;
  }

  private ensureBuilt(): void {
    if (!this.built) this.build();
  }

  private ensureLevels(): void {
    if (this.levelsDirty) this.computeLevels(this.topoSort());
  }
}
