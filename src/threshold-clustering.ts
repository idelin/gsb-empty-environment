/**
 * threshold-clustering
 *
 * 可撤销的阈值聚类库：按边权递增分批合并连通分量（Kruskal 式层次聚类），
 * 支持在任意阈值下查询聚类结果、撤回末批并追加新批。
 *
 * 核心不变式：
 *  - 同权重的边永远作为同一批（batch）整体生效，输入顺序不会产生不同的中间层；
 *  - 批次权重严格递增（追加与末批同权的边会自动并入末批）；
 *  - 每个批次提交后保存一份并查集快照，查询等价于"阈值 t 的子图"上的连通分量。
 */

/** 无权边（批次内），顶点为 0..vertexCount-1 的整数。 */
export type Edge = readonly [number, number];
/** 带权边：[u, v, weight]。 */
export type WeightedEdge = readonly [number, number, number];

export interface BatchInfo {
  readonly weight: number;
  readonly edges: readonly Edge[];
}

interface Snapshot {
  weight: number;
  edges: Edge[];
  /** 该批次生效后的并查集状态（按 size 合并，树高 O(log n)）。 */
  parent: number[];
  size: number[];
}

export class ThresholdClustering {
  private readonly n: number;
  private parent: number[];
  private size: number[];
  private snapshots: Snapshot[] = [];

  constructor(vertexCount: number) {
    if (!Number.isInteger(vertexCount) || vertexCount < 0) {
      throw new RangeError(`vertexCount 必须是非负整数，收到 ${vertexCount}`);
    }
    this.n = vertexCount;
    this.parent = Array.from({ length: vertexCount }, (_, i) => i);
    this.size = new Array<number>(vertexCount).fill(1);
  }

  get vertexCount(): number {
    return this.n;
  }

  get batchCount(): number {
    return this.snapshots.length;
  }

  /** 当前已提交批次的只读视图（防御性拷贝）。 */
  get batches(): readonly BatchInfo[] {
    return this.snapshots.map((s) => ({ weight: s.weight, edges: s.edges.slice() }));
  }

  /**
   * 追加一批同权边。
   *
   * - `weight` 大于末批权重：开启新批次；
   * - `weight` 等于末批权重：边并入末批（保证同权边原子生效，不产生中间层）；
   * - `weight` 小于末批权重：抛出 RangeError，应先 undoLastBatch() 再追加。
   *
   * 平行边与自环合法（并查集幂等）。
   */
  appendBatch(weight: number, edges: Iterable<Edge>): void {
    checkWeight(weight);
    const list = [...edges].map(([u, v]) => {
      this.checkVertex(u);
      this.checkVertex(v);
      return [u, v] as Edge;
    });

    const last = this.snapshots[this.snapshots.length - 1];
    if (last !== undefined) {
      if (weight < last.weight) {
        throw new RangeError(
          `批次权重必须非递减：末批权重为 ${last.weight}，收到 ${weight}。` +
            `请先 undoLastBatch() 再追加。`,
        );
      }
      if (weight === last.weight) {
        for (const [u, v] of list) this.union(u, v);
        last.edges.push(...list);
        last.parent = this.parent.slice();
        last.size = this.size.slice();
        return;
      }
    }

    for (const [u, v] of list) this.union(u, v);
    this.snapshots.push({
      weight,
      edges: list,
      parent: this.parent.slice(),
      size: this.size.slice(),
    });
  }

  /**
   * 追加一批带权边：内部按权重排序、同权分组后逐批提交。
   * 分组只依赖权重，与输入顺序无关。若某组权重小于当前末批权重则抛错。
   */
  appendEdges(edges: Iterable<WeightedEdge>): void {
    const sorted = [...edges].map((t) => this.checkWeightedEdge(t));
    sorted.sort((a, b) => a[2] - b[2]);
    let i = 0;
    while (i < sorted.length) {
      const w = sorted[i][2];
      const batch: Edge[] = [];
      while (i < sorted.length && sorted[i][2] === w) {
        batch.push([sorted[i][0], sorted[i][1]]);
        i++;
      }
      this.appendBatch(w, batch);
    }
  }

  /**
   * 从一组带权边构建聚类。边按权重排序后同权成批，
   * 因此任意输入顺序都得到完全相同的批次序列与聚类结果。
   */
  static fromEdges(vertexCount: number, edges: Iterable<WeightedEdge>): ThresholdClustering {
    const tc = new ThresholdClustering(vertexCount);
    tc.appendEdges(edges);
    return tc;
  }

  /** 撤回最后一批，返回被撤回的批次；没有批次时返回 undefined。 */
  undoLastBatch(): BatchInfo | undefined {
    const popped = this.snapshots.pop();
    if (popped === undefined) return undefined;
    const prev = this.snapshots[this.snapshots.length - 1];
    if (prev !== undefined) {
      this.parent = prev.parent.slice();
      this.size = prev.size.slice();
    } else {
      this.parent = Array.from({ length: this.n }, (_, i) => i);
      this.size = new Array<number>(this.n).fill(1);
    }
    return { weight: popped.weight, edges: popped.edges };
  }

  /**
   * 阈值 `threshold` 下的全部连通分量（含孤点单点集）。
   * 等价于在"权重 <= threshold 的边构成的子图"上求连通分量。
   */
  clustersAt(threshold: number): number[][] {
    const idx = this.locate(threshold);
    const parent = idx < 0 ? this.identity() : this.snapshots[idx].parent.slice();
    const groups = new Map<number, number[]>();
    for (let v = 0; v < this.n; v++) {
      const root = findCompress(parent, v);
      const g = groups.get(root);
      if (g === undefined) groups.set(root, [v]);
      else g.push(v);
    }
    return [...groups.values()];
  }

  /** 顶点 `v` 在阈值 `threshold` 下所在分量的全部成员。 */
  componentOf(v: number, threshold: number): number[] {
    this.checkVertex(v);
    const idx = this.locate(threshold);
    if (idx < 0) return [v];
    const parent = this.snapshots[idx].parent;
    const root = findReadonly(parent, v);
    const members: number[] = [];
    for (let u = 0; u < this.n; u++) {
      if (findReadonly(parent, u) === root) members.push(u);
    }
    return members;
  }

  /** 阈值 `threshold` 下的分量个数（含孤点）。 */
  componentCountAt(threshold: number): number {
    const idx = this.locate(threshold);
    if (idx < 0) return this.n;
    const parent = this.snapshots[idx].parent;
    let count = 0;
    for (let v = 0; v < this.n; v++) {
      if (findReadonly(parent, v) === v) count++;
    }
    return count;
  }

  /** u 与 v 在阈值 `threshold` 下是否连通。 */
  areConnected(u: number, v: number, threshold: number): boolean {
    this.checkVertex(u);
    this.checkVertex(v);
    if (u === v) return true;
    const idx = this.locate(threshold);
    if (idx < 0) return false;
    const parent = this.snapshots[idx].parent;
    return findReadonly(parent, u) === findReadonly(parent, v);
  }

  /**
   * u 与 v 进入同一分量的最早批次权重；始终不连通（或 u === v）时返回 null。
   * 即层次聚类中 u、v 所在子树合并那一层的权重。
   */
  mergeWeight(u: number, v: number): number | null {
    this.checkVertex(u);
    this.checkVertex(v);
    if (u === v) return null;
    const snaps = this.snapshots;
    if (snaps.length === 0) return null;
    const lastParent = snaps[snaps.length - 1].parent;
    if (findReadonly(lastParent, u) !== findReadonly(lastParent, v)) return null;
    let lo = 0;
    let hi = snaps.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const p = snaps[mid].parent;
      if (findReadonly(p, u) === findReadonly(p, v)) hi = mid;
      else lo = mid + 1;
    }
    return snaps[lo].weight;
  }

  /** 顶点 v 最早脱离单点集的批次权重；始终是孤点时返回 null。 */
  firstMergeWeight(v: number): number | null {
    this.checkVertex(v);
    const snaps = this.snapshots;
    if (snaps.length === 0) return null;
    const last = snaps[snaps.length - 1];
    if (last.size[findReadonly(last.parent, v)] <= 1) return null;
    let lo = 0;
    let hi = snaps.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const s = snaps[mid];
      if (s.size[findReadonly(s.parent, v)] > 1) hi = mid;
      else lo = mid + 1;
    }
    return snaps[lo].weight;
  }

  /** 已提交批次中最大的权重；无批次时返回 undefined。 */
  get lastBatchWeight(): number | undefined {
    const last = this.snapshots[this.snapshots.length - 1];
    return last === undefined ? undefined : last.weight;
  }

  private locate(threshold: number): number {
    // 最后一个 weight <= threshold 的批次下标；没有则 -1。
    let lo = 0;
    let hi = this.snapshots.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.snapshots[mid].weight <= threshold) lo = mid + 1;
      else hi = mid;
    }
    return lo - 1;
  }

  private identity(): number[] {
    return Array.from({ length: this.n }, (_, i) => i);
  }

  private union(u: number, v: number): void {
    let ru = this.findLive(u);
    let rv = this.findLive(v);
    if (ru === rv) return;
    if (this.size[ru] < this.size[rv]) {
      const t = ru;
      ru = rv;
      rv = t;
    }
    this.parent[rv] = ru;
    this.size[ru] += this.size[rv];
  }

  private findLive(x: number): number {
    let r = x;
    while (this.parent[r] !== r) {
      this.parent[r] = this.parent[this.parent[r]];
      r = this.parent[r];
    }
    return r;
  }

  private checkVertex(v: number): void {
    if (!Number.isInteger(v) || v < 0 || v >= this.n) {
      throw new RangeError(`顶点越界：${v}（合法范围 0..${this.n - 1}）`);
    }
  }

  private checkWeightedEdge(t: WeightedEdge): [number, number, number] {
    const [u, v, w] = t;
    this.checkVertex(u);
    this.checkVertex(v);
    checkWeight(w);
    return [u, v, w];
  }
}

function checkWeight(w: number): void {
  if (typeof w !== "number" || Number.isNaN(w)) {
    throw new TypeError(`权重必须是有限数值，收到 ${String(w)}`);
  }
}

/** 只读 find：不压缩路径。按 size 合并保证树高 O(log n)。 */
function findReadonly(parent: readonly number[], x: number): number {
  let r = x;
  while (parent[r] !== r) r = parent[r];
  return r;
}

/** 在可写副本上带路径压缩的 find。 */
function findCompress(parent: number[], x: number): number {
  let r = x;
  while (parent[r] !== r) {
    parent[r] = parent[parent[r]];
    r = parent[r];
  }
  return r;
}
