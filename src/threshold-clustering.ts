/**
 * 可撤销的阈值聚类库（单链接 / 按边权递增的连通分量合并）。
 *
 * 核心语义：
 * - 边按权重分批（weight level）生效；同一权重的所有边作为一批原子生效，
 *   输入顺序不会产生不同的中间层（批内边先排序再合并，结果确定）。
 * - 支持撤销最后一批（undoLastBatch）并追加新批（appendBatch）。
 * - 支持在任意阈值下查询连通分量成员，以及任意两点最早合并的权重。
 */

export interface WeightedEdge {
  u: number;
  v: number;
  w: number;
}

interface LevelSnapshot {
  weight: number;
  /** 该批生效后的并查集状态（parent/size 数组副本）。 */
  parent: number[];
  size: number[];
}

export class ThresholdClustering {
  readonly vertexCount: number;
  /** 已追加的全部边（按追加顺序），用于阈值子图核对等场景。 */
  private readonly edges: WeightedEdge[] = [];
  /** 权重层，按 weight 严格递增。 */
  private levels: LevelSnapshot[] = [];
  /** 当前（所有批生效后）的并查集状态。 */
  private parent: number[];
  private size: number[];

  constructor(vertexCount: number) {
    if (!Number.isInteger(vertexCount) || vertexCount < 0) {
      throw new RangeError("vertexCount 必须是非负整数");
    }
    this.vertexCount = vertexCount;
    this.parent = identity(vertexCount);
    this.size = new Array<number>(vertexCount).fill(1);
  }

  /** 当前批次数（权重层数）。 */
  get batchCount(): number {
    return this.levels.length;
  }

  /** 各批权重，按递增顺序。 */
  get batchWeights(): number[] {
    return this.levels.map((l) => l.weight);
  }

  /** 已追加的全部边（只读副本）。 */
  get allEdges(): WeightedEdge[] {
    return this.edges.slice();
  }

  /**
   * 追加一批边。批内可按权重分组；每个不同权重构成一个新层。
   * 约束：批内最小权重不得小于当前最后一层的权重；
   * 等于最后一层权重时并入该层（同权边作为同一批生效）。
   */
  appendBatch(edges: Iterable<WeightedEdge>): void {
    const byWeight = new Map<number, WeightedEdge[]>();
    for (const e of edges) {
      this.validateEdge(e);
      const list = byWeight.get(e.w);
      if (list) list.push(e);
      else byWeight.set(e.w, [e]);
    }
    if (byWeight.size === 0) return;

    const weights = [...byWeight.keys()].sort((a, b) => a - b);
    const lastWeight = this.levels.length
      ? this.levels[this.levels.length - 1].weight
      : -Infinity;
    if (weights[0] < lastWeight) {
      throw new Error(
        `新批权重 ${weights[0]} 小于当前最后一批权重 ${lastWeight}；` +
          `请先 undoLastBatch() 再追加`
      );
    }

    for (const w of weights) {
      // 批内排序：保证同权边集合的合并结果与输入顺序无关。
      const group = byWeight.get(w)!.slice().sort(compareEdges);
      if (w === lastWeight) {
        // 并入现有末层：重放该层全部边后重新生成快照。
        for (const e of group) this.union(e.u, e.v);
        const top = this.levels[this.levels.length - 1];
        top.parent = this.parent.slice();
        top.size = this.size.slice();
      } else {
        for (const e of group) this.union(e.u, e.v);
        this.levels.push({
          weight: w,
          parent: this.parent.slice(),
          size: this.size.slice(),
        });
      }
      for (const e of group) this.edges.push(e);
    }
  }

  /** 撤销最后一批。返回是否确实撤销了一批。 */
  undoLastBatch(): boolean {
    const popped = this.levels.pop();
    if (!popped) return false;
    // 从边列表中移除该权重的边。
    while (
      this.edges.length &&
      this.edges[this.edges.length - 1].w === popped.weight
    ) {
      this.edges.pop();
    }
    const top = this.levels[this.levels.length - 1];
    this.parent = top ? top.parent.slice() : identity(this.vertexCount);
    this.size = top ? top.size.slice() : new Array<number>(this.vertexCount).fill(1);
    return true;
  }

  /** 阈值 threshold 下的全部连通分量（含孤点单点分量），成员升序。 */
  componentsAt(threshold: number): number[][] {
    const { parent } = this.stateAt(threshold);
    const groups = new Map<number, number[]>();
    for (let v = 0; v < this.vertexCount; v++) {
      const r = findRoot(parent, v);
      const list = groups.get(r);
      if (list) list.push(v);
      else groups.set(r, [v]);
    }
    return [...groups.values()]
      .map((members) => members.sort((a, b) => a - b))
      .sort((a, b) => a[0] - b[0]);
  }

  /** 阈值 threshold 下 vertex 所在分量的成员（升序）。 */
  membersAt(vertex: number, threshold: number): number[] {
    this.validateVertex(vertex);
    const { parent } = this.stateAt(threshold);
    const root = findRoot(parent, vertex);
    const members: number[] = [];
    for (let v = 0; v < this.vertexCount; v++) {
      if (findRoot(parent, v) === root) members.push(v);
    }
    return members;
  }

  /** 阈值 threshold 下 u 与 v 是否连通。 */
  connectedAt(u: number, v: number, threshold: number): boolean {
    this.validateVertex(u);
    this.validateVertex(v);
    const { parent } = this.stateAt(threshold);
    return findRoot(parent, u) === findRoot(parent, v);
  }

  /**
   * u 与 v 最早合并（进入同一分量）的权重。
   * u === v 时返回 -Infinity（任何阈值下都同分量）；
   * 永不连通时返回 Infinity。
   */
  mergeWeight(u: number, v: number): number {
    this.validateVertex(u);
    this.validateVertex(v);
    if (u === v) return -Infinity;
    const n = this.levels.length;
    if (
      n === 0 ||
      findRoot(this.parent, u) !== findRoot(this.parent, v)
    ) {
      return Infinity;
    }
    // 二分第一个使 u、v 连通的层（连通性随层单调）。
    let lo = 0;
    let hi = n - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (findRoot(this.levels[mid].parent, u) === findRoot(this.levels[mid].parent, v)) {
        hi = mid;
      } else {
        lo = mid + 1;
      }
    }
    return this.levels[lo].weight;
  }

  /** 当前（全部批生效后）的连通分量。 */
  components(): number[][] {
    return this.componentsAt(Infinity);
  }

  private stateAt(threshold: number): LevelSnapshot {
    // 二分最后一个 weight <= threshold 的层。
    let lo = -1;
    let hi = this.levels.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (this.levels[mid].weight <= threshold) lo = mid;
      else hi = mid - 1;
    }
    if (lo >= 0) return this.levels[lo];
    return {
      weight: -Infinity,
      parent: identity(this.vertexCount),
      size: new Array<number>(this.vertexCount).fill(1),
    };
  }

  private union(a: number, b: number): void {
    let ra = findRoot(this.parent, a);
    let rb = findRoot(this.parent, b);
    if (ra === rb) return; // 平行边 / 已连通
    if (this.size[ra] < this.size[rb]) {
      const t = ra;
      ra = rb;
      rb = t;
    }
    this.parent[rb] = ra;
    this.size[ra] += this.size[rb];
  }

  private validateEdge(e: WeightedEdge): void {
    this.validateVertex(e.u);
    this.validateVertex(e.v);
    if (typeof e.w !== "number" || Number.isNaN(e.w)) {
      throw new TypeError(`非法边权: ${e.w}`);
    }
  }

  private validateVertex(v: number): void {
    if (!Number.isInteger(v) || v < 0 || v >= this.vertexCount) {
      throw new RangeError(`顶点 ${v} 超出范围 [0, ${this.vertexCount})`);
    }
  }
}

function identity(n: number): number[] {
  const p = new Array<number>(n);
  for (let i = 0; i < n; i++) p[i] = i;
  return p;
}

function findRoot(parent: number[], v: number): number {
  // 纯查找，不做路径压缩：查询直接读取层快照，不能改写快照；
  // 按大小合并已保证树高 O(log n)。
  let r = v;
  while (parent[r] !== r) r = parent[r];
  return r;
}

function compareEdges(a: WeightedEdge, b: WeightedEdge): number {
  const aLo = Math.min(a.u, a.v);
  const aHi = Math.max(a.u, a.v);
  const bLo = Math.min(b.u, b.v);
  const bHi = Math.max(b.u, b.v);
  return aLo - bLo || aHi - bHi || a.w - b.w;
}
