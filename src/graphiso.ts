/**
 * 带顶点颜色的无向多重图同构检测器。
 *
 * 算法：颜色细化（1-WL，边重数参与签名）+ 个体化-细化（IR）搜索，
 * 搜索过程中利用已发现的自同构做轨道剪枝。叶子的规范序给出规范编码，
 * 规范编码在任意顶点重标号下不变。
 *
 * 结果三态：isomorphic（附可核对映射）/ not-isomorphic（附双方规范编码作证书）/
 * unknown（搜索预算耗尽，绝不为节省预算而误判）。
 */

export interface ColoredGraph {
  readonly n: number;
  /** colors[v]：顶点 v 的颜色，非负整数。 */
  readonly colors: readonly number[];
  /** adj[u].get(v)：边 {u,v} 的重数（自环存在 adj[u].get(u)）。 */
  readonly adj: ReadonlyArray<ReadonlyMap<number, number>>;
}

/** 由边表构建多重图；平行边累计重数，允许自环。 */
export function createGraph(
  n: number,
  colors: readonly number[],
  edges: ReadonlyArray<readonly [number, number]>,
): ColoredGraph {
  if (!Number.isInteger(n) || n < 0) throw new Error("n must be a non-negative integer");
  if (colors.length !== n) throw new Error("colors.length must equal n");
  for (const c of colors) {
    if (!Number.isInteger(c) || c < 0) throw new Error("colors must be non-negative integers");
  }
  const adj: Map<number, number>[] = Array.from({ length: n }, () => new Map());
  const add = (u: number, v: number): void => {
    adj[u].set(v, (adj[u].get(v) ?? 0) + 1);
  };
  for (const [u, v] of edges) {
    if (!Number.isInteger(u) || !Number.isInteger(v) || u < 0 || v < 0 || u >= n || v >= n) {
      throw new Error(`edge endpoint out of range: [${u}, ${v}]`);
    }
    add(u, v);
    if (u !== v) add(v, u);
  }
  return { n, colors: colors.slice(), adj };
}

/** 重标号：返回新图，新标号 i 对应旧顶点 perm[i]。 */
export function relabel(g: ColoredGraph, perm: readonly number[]): ColoredGraph {
  const n = g.n;
  if (perm.length !== n) throw new Error("perm.length must equal n");
  const seen = new Set(perm);
  if (seen.size !== n) throw new Error("perm must be a permutation of 0..n-1");
  const indexOf = new Array<number>(n);
  for (let i = 0; i < n; i++) indexOf[perm[i]] = i;
  const colors = perm.map((old) => g.colors[old]);
  const adj: Map<number, number>[] = Array.from({ length: n }, () => new Map());
  for (let i = 0; i < n; i++) {
    for (const [oldJ, m] of g.adj[perm[i]]) {
      adj[i].set(indexOf[oldJ], m);
    }
  }
  return { n, colors, adj };
}

/** 独立核对器：验证 mapping 是否为 g1 -> g2 的保色同构。 */
export function verifyIsomorphism(
  g1: ColoredGraph,
  g2: ColoredGraph,
  mapping: readonly number[],
): boolean {
  const n = g1.n;
  if (g2.n !== n || mapping.length !== n) return false;
  const image = new Set<number>();
  for (const m of mapping) {
    if (!Number.isInteger(m) || m < 0 || m >= n) return false;
    image.add(m);
  }
  if (image.size !== n) return false;
  for (let u = 0; u < n; u++) {
    if (g1.colors[u] !== g2.colors[mapping[u]]) return false;
  }
  for (let u = 0; u < n; u++) {
    for (const [v, m] of g1.adj[u]) {
      if (v < u) continue; // 每个无序对（含自环）只查一次
      if ((g2.adj[mapping[u]].get(mapping[v]) ?? 0) !== m) return false;
    }
  }
  return true;
}

// ---------------------------------------------------------------------------
// 内部实现
// ---------------------------------------------------------------------------

const DEFAULT_BUDGET = 1_000_000; // 搜索树节点数上限

class BudgetExceeded extends Error {}

function compareArrays(a: readonly number[], b: readonly number[]): number {
  const len = Math.min(a.length, b.length);
  for (let i = 0; i < len; i++) {
    if (a[i] !== b[i]) return a[i] < b[i] ? -1 : 1;
  }
  return a.length - b.length;
}

/** 初始着色：把原始颜色值规范化为 0..k-1（按值排序），与顶点编号无关。 */
function initialColoring(g: ColoredGraph): number[] {
  const distinct = [...new Set(g.colors)].sort((a, b) => a - b);
  const id = new Map<number, number>();
  distinct.forEach((c, i) => id.set(c, i));
  return g.colors.map((c) => id.get(c)!);
}

/**
 * 颜色细化：反复按 (自身颜色, 排序后的 [邻居颜色, 边重数] 序列) 分裂颜色类，
 * 直到稳定。返回的着色只依赖图结构，与顶点编号顺序无关。
 */
function refine(g: ColoredGraph, coloring: number[]): number[] {
  let color = coloring.slice();
  let cells = color.length === 0 ? 0 : Math.max(...color) + 1;
  for (;;) {
    const keys: number[][] = new Array(g.n);
    for (let v = 0; v < g.n; v++) {
      const pairs: number[][] = [];
      for (const [u, m] of g.adj[v]) pairs.push([color[u], m]);
      pairs.sort(compareArrays);
      const key = [color[v]];
      for (const p of pairs) key.push(p[0], p[1]);
      keys[v] = key;
    }
    const order = Array.from({ length: g.n }, (_, i) => i).sort((a, b) => {
      if (color[a] !== color[b]) return color[a] - color[b];
      return compareArrays(keys[a], keys[b]);
    });
    const next = new Array<number>(g.n);
    let nextCells = 0;
    for (let i = 0; i < order.length; i++) {
      if (i > 0) {
        const prev = order[i - 1];
        const cur = order[i];
        if (color[prev] !== color[cur] || compareArrays(keys[prev], keys[cur]) !== 0) {
          nextCells++;
        }
      }
      next[order[i]] = nextCells;
    }
    nextCells++;
    if (nextCells === cells) return next; // 细化只会分裂，类数不变即划分稳定
    color = next;
    cells = nextCells;
  }
}

/** 按着色把顶点分格，返回按颜色编号排序的格子数组。 */
function cellsOf(coloring: readonly number[]): number[][] {
  const cells: number[][] = [];
  for (let v = 0; v < coloring.length; v++) {
    (cells[coloring[v]] ??= []).push(v);
  }
  return cells;
}

/** 叶子证书：规范序下的颜色序列 + 上三角（含对角线）重数矩阵。 */
function certificate(g: ColoredGraph, perm: readonly number[]): number[] {
  const cert: number[] = [g.n];
  for (let i = 0; i < g.n; i++) cert.push(g.colors[perm[i]]);
  for (let i = 0; i < g.n; i++) {
    for (let j = i; j < g.n; j++) {
      cert.push(g.adj[perm[i]].get(perm[j]) ?? 0);
    }
  }
  return cert;
}

class Canonizer {
  private readonly g: ColoredGraph;
  private readonly budget: number;
  private best: number[] | null = null;
  private bestPerm: number[] | null = null;
  nodesExplored = 0;

  constructor(g: ColoredGraph, budget: number) {
    this.g = g;
    this.budget = budget;
  }

  run(): { cert: number[]; perm: number[] } {
    this.search(refine(this.g, initialColoring(this.g)));
    if (this.best === null || this.bestPerm === null) {
      throw new Error("unreachable: search produced no leaf");
    }
    return { cert: this.best, perm: this.bestPerm };
  }

  /**
   * IR 搜索。返回子树中发现的自同构（供父节点做轨道剪枝）。
   * 规范叶 = 证书字典序最小的叶。
   */
  private search(coloring: number[]): number[][] {
    this.nodesExplored++;
    if (this.nodesExplored > this.budget) throw new BudgetExceeded();
    const g = this.g;
    const cells = cellsOf(coloring);
    let target = -1;
    for (let i = 0; i < cells.length; i++) {
      if (cells[i].length > 1) {
        target = i;
        break;
      }
    }

    if (target === -1) {
      // 离散划分：叶子
      const perm: number[] = [];
      for (const cell of cells) perm.push(cell[0]);
      const cert = certificate(g, perm);
      if (this.best === null || compareArrays(cert, this.best) < 0) {
        this.best = cert;
        this.bestPerm = perm;
        return [];
      }
      if (compareArrays(cert, this.best) === 0 && this.bestPerm !== null) {
        // 与当前最优叶证书相同：合成一个自同构 bestPerm[i] -> perm[i]
        const bestPerm = this.bestPerm;
        const inv = new Array<number>(g.n);
        for (let i = 0; i < g.n; i++) inv[bestPerm[i]] = i;
        const gamma = new Array<number>(g.n);
        for (let v = 0; v < g.n; v++) gamma[v] = perm[inv[v]];
        return [gamma];
      }
      return [];
    }

    const cell = cells[target];
    const indexOf = new Map<number, number>();
    cell.forEach((v, i) => indexOf.set(v, i));
    // 目标格上的并查集，用于轨道剪枝
    const parent = cell.map((_, i) => i);
    const find = (i: number): number => {
      while (parent[i] !== i) {
        parent[i] = parent[parent[i]];
        i = parent[i];
      }
      return i;
    };
    const union = (a: number, b: number): void => {
      const ra = find(a);
      const rb = find(b);
      if (ra !== rb) parent[rb] = ra;
    };

    const processed = new Set<number>();
    const autos: number[][] = [];
    for (let i = 0; i < cell.length; i++) {
      const root = find(i);
      if (processed.has(root)) continue; // 与已搜分支同轨道，证书多重集相同，剪枝
      processed.add(root);
      const v = cell[i];
      // 个体化：把 v 单独成格，插到目标格之前
      const child = new Array<number>(g.n);
      for (let u = 0; u < g.n; u++) {
        child[u] = coloring[u] < target ? coloring[u] : coloring[u] + 1;
      }
      child[v] = target;
      const childAutos = this.search(refine(g, child));
      for (const gamma of childAutos) {
        for (let j = 0; j < cell.length; j++) {
          const j2 = indexOf.get(gamma[cell[j]]);
          if (j2 !== undefined) union(j, j2);
        }
      }
      autos.push(...childAutos);
    }
    return autos;
  }
}

function canonicalString(cert: readonly number[]): string {
  return cert.join(",");
}

export type CanonicalResult =
  | { status: "ok"; canonical: string; labeling: number[]; nodesExplored: number }
  | { status: "unknown"; reason: string; nodesExplored: number };

/**
 * 计算规范编码。labeling[i] = 规范序第 i 位的原始顶点编号。
 * 同一抽象图的任意顶点重标号得到完全相同的 canonical 字符串。
 */
export function canonicalForm(g: ColoredGraph, budget: number = DEFAULT_BUDGET): CanonicalResult {
  const c = new Canonizer(g, budget);
  try {
    const { cert, perm } = c.run();
    return { status: "ok", canonical: canonicalString(cert), labeling: perm, nodesExplored: c.nodesExplored };
  } catch (e) {
    if (e instanceof BudgetExceeded) {
      return {
        status: "unknown",
        reason: `search budget ${budget} exceeded after ${c.nodesExplored} nodes`,
        nodesExplored: c.nodesExplored,
      };
    }
    throw e;
  }
}

export type IsoResult =
  | { status: "isomorphic"; mapping: number[]; canonical: string; nodesExplored: number }
  | {
      status: "not-isomorphic";
      reason: string;
      canonical1?: string;
      canonical2?: string;
      nodesExplored: number;
    }
  | { status: "unknown"; reason: string; nodesExplored: number };

function sortedHistogram(values: readonly number[]): number[] {
  return values.slice().sort((a, b) => a - b);
}

function totalMultiplicity(g: ColoredGraph): number {
  let total = 0;
  for (const m of g.adj) for (const k of m.values()) total += k;
  return total;
}

/**
 * 同构判定。预算只限制搜索规模；预算耗尽返回 unknown，绝不猜测。
 * not-isomorphic 结果附带双方规范编码，可用 canonicalForm 独立复算核对。
 */
export function isomorphic(g1: ColoredGraph, g2: ColoredGraph, budget: number = DEFAULT_BUDGET): IsoResult {
  // 健全的必要条件快速否决（只用于否决，绝不用于肯定）
  if (g1.n !== g2.n) {
    return { status: "not-isomorphic", reason: "vertex count differs", nodesExplored: 0 };
  }
  const h1 = sortedHistogram(g1.colors);
  const h2 = sortedHistogram(g2.colors);
  if (compareArrays(h1, h2) !== 0) {
    return { status: "not-isomorphic", reason: "color histogram differs", nodesExplored: 0 };
  }
  if (totalMultiplicity(g1) !== totalMultiplicity(g2)) {
    return { status: "not-isomorphic", reason: "edge multiplicity total differs", nodesExplored: 0 };
  }

  const c1 = canonicalForm(g1, budget);
  if (c1.status === "unknown") return { status: "unknown", reason: `g1: ${c1.reason}`, nodesExplored: c1.nodesExplored };
  const c2 = canonicalForm(g2, budget);
  if (c2.status === "unknown") return { status: "unknown", reason: `g2: ${c2.reason}`, nodesExplored: c2.nodesExplored };
  const nodesExplored = c1.nodesExplored + c2.nodesExplored;

  if (c1.canonical !== c2.canonical) {
    return {
      status: "not-isomorphic",
      reason: "canonical forms differ",
      canonical1: c1.canonical,
      canonical2: c2.canonical,
      nodesExplored,
    };
  }

  // 规范序第 i 位互相对应，得到 g1 -> g2 的映射
  const mapping = new Array<number>(g1.n);
  for (let i = 0; i < g1.n; i++) mapping[c1.labeling[i]] = c2.labeling[i];
  if (!verifyIsomorphism(g1, g2, mapping)) {
    throw new Error("internal error: canonical mapping failed verification");
  }
  return { status: "isomorphic", mapping, canonical: c1.canonical, nodesExplored };
}
