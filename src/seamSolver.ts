/**
 * Seam solver for the overlap region of two aligned images.
 *
 * Every pixel of the overlap must be assigned to source image 'A' or 'B'.
 * The objective is the sum of:
 *   - source costs: costA[y][x] / costB[y][x] paid when the pixel takes that source;
 *   - switch costs: a penalty for every 4-connected neighbour pair whose labels differ
 *     (uniform `switchCost` plus optional per-edge `horizontalSwitch` / `verticalSwitch`).
 *
 * Boundary pixels may be pinned to a fixed source via `fixed`.
 *
 * This is a binary submodular labelling problem, solved exactly with an
 * s-t min-cut (Dinic max-flow). Among all optimal labellings the solver
 * returns the canonical A-maximal one (ties resolve towards source 'A'),
 * which makes the output deterministic.
 */

export type SourceLabel = 'A' | 'B';

export interface SeamProblem {
  /** Overlap width in pixels (>= 0). */
  width: number;
  /** Overlap height in pixels (>= 0). */
  height: number;
  /** costA[y][x]: cost of taking pixel (x, y) from image A. Non-negative, finite. */
  costA: number[][];
  /** costB[y][x]: cost of taking pixel (x, y) from image B. Non-negative, finite. */
  costB: number[][];
  /** Uniform penalty for every adjacent pair with different sources. Default 0. */
  switchCost?: number;
  /** horizontalSwitch[y][x]: extra penalty between (x, y) and (x + 1, y). Size height x (width - 1). */
  horizontalSwitch?: number[][];
  /** verticalSwitch[y][x]: extra penalty between (x, y) and (x, y + 1). Size (height - 1) x width. */
  verticalSwitch?: number[][];
  /** fixed[y][x]: 'A' | 'B' pins the pixel source, null leaves it free. */
  fixed?: (SourceLabel | null)[][];
}

export interface SeamCostBreakdown {
  /** Total source cost paid for pixels taken from A. */
  sourceCostA: number;
  /** Total source cost paid for pixels taken from B. */
  sourceCostB: number;
  /** sourceCostA + sourceCostB. */
  sourceCost: number;
  /** Total switch penalty over adjacent pairs with different sources. */
  switchCost: number;
  /** Number of adjacent pairs whose sources differ. */
  switchCount: number;
  /** sourceCost + switchCost. */
  total: number;
}

export interface SeamResult {
  width: number;
  height: number;
  /** labels[y][x]: chosen source for pixel (x, y). */
  labels: SourceLabel[][];
  costs: SeamCostBreakdown;
}

const EPS = 1e-9;

interface Normalized {
  width: number;
  height: number;
  costA: number[][];
  costB: number[][];
  /** Horizontal edge weights, height rows of width - 1. */
  hW: number[][];
  /** Vertical edge weights, height - 1 rows of width. */
  vW: number[][];
  fixed: (SourceLabel | null)[][];
}

function assertGrid(grid: number[][], name: string, rows: number, cols: number): void {
  if (!Array.isArray(grid) || grid.length !== rows) {
    throw new TypeError(`${name} must be an array of ${rows} rows, got ${Array.isArray(grid) ? grid.length : typeof grid}`);
  }
  for (let y = 0; y < rows; y++) {
    const row = grid[y];
    if (!Array.isArray(row) || row.length !== cols) {
      throw new TypeError(`${name}[${y}] must be an array of ${cols} entries, got ${Array.isArray(row) ? row.length : typeof row}`);
    }
    for (let x = 0; x < cols; x++) {
      const v = row[x];
      if (typeof v !== 'number' || !Number.isFinite(v) || v < 0) {
        throw new RangeError(`${name}[${y}][${x}] must be a non-negative finite number, got ${String(v)}`);
      }
    }
  }
}

function normalize(problem: SeamProblem): Normalized {
  if (problem === null || typeof problem !== 'object') {
    throw new TypeError('problem must be an object');
  }
  const { width, height } = problem;
  if (!Number.isInteger(width) || !Number.isInteger(height) || (width as number) < 0 || (height as number) < 0) {
    throw new TypeError(`width and height must be non-negative integers, got ${String(width)} x ${String(height)}`);
  }

  const uniform = problem.switchCost ?? 0;
  if (typeof uniform !== 'number' || !Number.isFinite(uniform) || uniform < 0) {
    throw new RangeError(`switchCost must be a non-negative finite number, got ${String(uniform)}`);
  }

  // Degenerate (no-overlap) region: accept empty/absent grids, nothing to solve.
  if (width === 0 || height === 0) {
    return {
      width,
      height,
      costA: Array.from({ length: height }, () => []),
      costB: Array.from({ length: height }, () => []),
      hW: Array.from({ length: height }, () => []),
      vW: Array.from({ length: Math.max(0, height - 1) }, () => []),
      fixed: Array.from({ length: height }, () => new Array<SourceLabel | null>(0)),
    };
  }

  assertGrid(problem.costA, 'costA', height, width);
  assertGrid(problem.costB, 'costB', height, width);

  const hW = Array.from({ length: height }, () => new Array<number>(width - 1).fill(uniform));
  if (problem.horizontalSwitch !== undefined) {
    assertGrid(problem.horizontalSwitch, 'horizontalSwitch', height, width - 1);
    for (let y = 0; y < height; y++) {
      for (let x = 0; x < width - 1; x++) hW[y][x] += problem.horizontalSwitch[y][x];
    }
  }

  const vW = Array.from({ length: height - 1 }, () => new Array<number>(width).fill(uniform));
  if (problem.verticalSwitch !== undefined) {
    assertGrid(problem.verticalSwitch, 'verticalSwitch', height - 1, width);
    for (let y = 0; y < height - 1; y++) {
      for (let x = 0; x < width; x++) vW[y][x] += problem.verticalSwitch[y][x];
    }
  }

  const fixed: (SourceLabel | null)[][] = Array.from({ length: height }, () => new Array<SourceLabel | null>(width).fill(null));
  if (problem.fixed !== undefined) {
    if (!Array.isArray(problem.fixed) || problem.fixed.length !== height) {
      throw new TypeError(`fixed must be an array of ${height} rows`);
    }
    for (let y = 0; y < height; y++) {
      const row = problem.fixed[y];
      if (!Array.isArray(row) || row.length !== width) {
        throw new TypeError(`fixed[${y}] must be an array of ${width} entries`);
      }
      for (let x = 0; x < width; x++) {
        const v = row[x];
        if (v !== 'A' && v !== 'B' && v !== null && v !== undefined) {
          throw new TypeError(`fixed[${y}][${x}] must be 'A', 'B' or null, got ${String(v)}`);
        }
        fixed[y][x] = v ?? null;
      }
    }
  }

  return { width, height, costA: problem.costA, costB: problem.costB, hW, vW, fixed };
}

function assertLabels(labels: SourceLabel[][], height: number, width: number): void {
  if (!Array.isArray(labels) || labels.length !== height) {
    throw new TypeError(`labels must be an array of ${height} rows`);
  }
  for (let y = 0; y < height; y++) {
    const row = labels[y];
    if (!Array.isArray(row) || row.length !== width) {
      throw new TypeError(`labels[${y}] must be an array of ${width} entries`);
    }
    for (let x = 0; x < width; x++) {
      if (row[x] !== 'A' && row[x] !== 'B') {
        throw new TypeError(`labels[${y}][${x}] must be 'A' or 'B', got ${String(row[x])}`);
      }
    }
  }
}

function costOfLabels(norm: Normalized, labels: SourceLabel[][]): SeamCostBreakdown {
  let sourceCostA = 0;
  let sourceCostB = 0;
  let switchCost = 0;
  let switchCount = 0;
  for (let y = 0; y < norm.height; y++) {
    for (let x = 0; x < norm.width; x++) {
      if (labels[y][x] === 'A') sourceCostA += norm.costA[y][x];
      else sourceCostB += norm.costB[y][x];
      if (x + 1 < norm.width && labels[y][x] !== labels[y][x + 1]) {
        switchCost += norm.hW[y][x];
        switchCount++;
      }
      if (y + 1 < norm.height && labels[y][x] !== labels[y + 1][x]) {
        switchCost += norm.vW[y][x];
        switchCount++;
      }
    }
  }
  const sourceCost = sourceCostA + sourceCostB;
  return { sourceCostA, sourceCostB, sourceCost, switchCost, switchCount, total: sourceCost + switchCost };
}

/** Evaluate the full cost breakdown of a given labelling. */
export function evaluateCost(problem: SeamProblem, labels: SourceLabel[][]): SeamCostBreakdown {
  const norm = normalize(problem);
  assertLabels(labels, norm.height, norm.width);
  return costOfLabels(norm, labels);
}

interface Edge {
  to: number;
  rev: number;
  cap: number;
}

/** Dinic max-flow with deterministic (insertion-order) traversal. */
class MaxFlow {
  private readonly n: number;
  private readonly graph: Edge[][];
  private readonly level: number[];
  private readonly it: number[];

  constructor(n: number) {
    this.n = n;
    this.graph = Array.from({ length: n }, () => []);
    this.level = new Array<number>(n).fill(0);
    this.it = new Array<number>(n).fill(0);
  }

  addEdge(from: number, to: number, cap: number): void {
    this.graph[from].push({ to, rev: this.graph[to].length, cap });
    this.graph[to].push({ to: from, rev: this.graph[from].length - 1, cap: 0 });
  }

  maxFlow(s: number, t: number): number {
    let flow = 0;
    while (this.bfs(s, t)) {
      this.it.fill(0);
      let f: number;
      while ((f = this.dfs(s, t, Infinity)) > EPS) flow += f;
    }
    return flow;
  }

  private bfs(s: number, t: number): boolean {
    this.level.fill(-1);
    this.level[s] = 0;
    const queue: number[] = [s];
    for (let head = 0; head < queue.length; head++) {
      const v = queue[head];
      for (const e of this.graph[v]) {
        if (e.cap > EPS && this.level[e.to] < 0) {
          this.level[e.to] = this.level[v] + 1;
          queue.push(e.to);
        }
      }
    }
    return this.level[t] >= 0;
  }

  private dfs(v: number, t: number, f: number): number {
    if (v === t) return f;
    const edges = this.graph[v];
    for (; this.it[v] < edges.length; this.it[v]++) {
      const e = edges[this.it[v]];
      if (e.cap > EPS && this.level[e.to] === this.level[v] + 1) {
        const d = this.dfs(e.to, t, Math.min(f, e.cap));
        if (d > EPS) {
          e.cap -= d;
          this.graph[e.to][e.rev].cap += d;
          return d;
        }
      }
    }
    return 0;
  }

  /** Nodes that can reach `t` in the residual graph (reverse BFS from t). */
  reachableTo(t: number): boolean[] {
    const seen = new Array<boolean>(this.n).fill(false);
    seen[t] = true;
    const queue: number[] = [t];
    for (let head = 0; head < queue.length; head++) {
      const v = queue[head];
      for (let u = 0; u < this.n; u++) {
        if (seen[u]) continue;
        for (const e of this.graph[u]) {
          if (e.to === v && e.cap > EPS) {
            seen[u] = true;
            queue.push(u);
            break;
          }
        }
      }
    }
    return seen;
  }
}

/**
 * Solve the seam labelling exactly.
 *
 * Returns the A-maximal minimum-cost labelling: among all labellings that
 * attain the optimum, pixels are assigned to source 'A' whenever possible,
 * so ties are broken deterministically.
 */
export function solveSeam(problem: SeamProblem): SeamResult {
  const norm = normalize(problem);
  const { width, height } = norm;

  if (width === 0 || height === 0) {
    return {
      width,
      height,
      labels: Array.from({ length: height }, () => []),
      costs: { sourceCostA: 0, sourceCostB: 0, sourceCost: 0, switchCost: 0, switchCount: 0, total: 0 },
    };
  }

  const n = width * height;
  const s = n;
  const t = n + 1;
  const mf = new MaxFlow(n + 2);

  let finiteSum = 0;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) finiteSum += norm.costA[y][x] + norm.costB[y][x];
    for (let x = 0; x < width - 1; x++) finiteSum += norm.hW[y][x];
  }
  for (let y = 0; y < height - 1; y++) {
    for (let x = 0; x < width; x++) finiteSum += norm.vW[y][x];
  }
  const INF = finiteSum + 1;

  const id = (x: number, y: number): number => y * width + x;
  for (let y = 0; y < height; y++) {
    for (let x = 0; x < width; x++) {
      const p = id(x, y);
      // Source side of the cut = label A, sink side = label B.
      mf.addEdge(s, p, norm.costB[y][x]);
      mf.addEdge(p, t, norm.costA[y][x]);
      if (norm.fixed[y][x] === 'A') mf.addEdge(s, p, INF);
      else if (norm.fixed[y][x] === 'B') mf.addEdge(p, t, INF);
      if (x + 1 < width) {
        const w = norm.hW[y][x];
        mf.addEdge(p, id(x + 1, y), w);
        mf.addEdge(id(x + 1, y), p, w);
      }
      if (y + 1 < height) {
        const w = norm.vW[y][x];
        mf.addEdge(p, id(x, y + 1), w);
        mf.addEdge(id(x, y + 1), p, w);
      }
    }
  }

  mf.maxFlow(s, t);

  // Canonical min cut: pixels that can still reach the sink in the residual
  // graph form the sink side (label B); everything else is labelled A.
  const sinkSide = mf.reachableTo(t);
  const labels: SourceLabel[][] = [];
  for (let y = 0; y < height; y++) {
    const row: SourceLabel[] = new Array<SourceLabel>(width);
    for (let x = 0; x < width; x++) row[x] = sinkSide[id(x, y)] ? 'B' : 'A';
    labels.push(row);
  }

  return { width, height, labels, costs: costOfLabels(norm, labels) };
}
