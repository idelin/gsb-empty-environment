import { MinHeap } from './heap.ts';

/** Sentinel distance for unassigned cells. */
export const BIG = 0x3fffffff;

/** Thrown when the step budget of an update is exhausted. */
export class BudgetExceeded extends Error {
  constructor() {
    super('budget-exceeded');
    this.name = 'BudgetExceeded';
  }
}

export interface Budget {
  left: number;
}

export function spend(budget: Budget | undefined, n = 1): void {
  if (!budget) return;
  budget.left -= n;
  if (budget.left < 0) throw new BudgetExceeded();
}

export function eachNeighbor(idx: number, w: number, h: number, fn: (n: number) => void): void {
  const x = idx % w;
  if (x > 0) fn(idx - 1);
  if (x < w - 1) fn(idx + 1);
  if (idx >= w) fn(idx - w);
  if (idx < w * (h - 1)) fn(idx + w);
}

export interface PlateauInfo {
  cells: number[];
  minimum: boolean;
}

/**
 * Returns the maximal 4-connected equal-height plateau containing `start`
 * and whether it is a regional minimum (no strictly lower neighbor).
 */
export function plateauInfo(heights: Int32Array, w: number, h: number, start: number): PlateauInfo {
  const level = heights[start];
  const cells: number[] = [];
  const seen = new Set<number>([start]);
  const queue: number[] = [start];
  let minimum = true;
  while (queue.length > 0) {
    const c = queue.pop() as number;
    cells.push(c);
    eachNeighbor(c, w, h, (n) => {
      if (heights[n] < level) minimum = false;
      if (heights[n] === level && !seen.has(n)) {
        seen.add(n);
        queue.push(n);
      }
    });
  }
  return { cells, minimum };
}

export function keyLess(l1: number, d1: number, t1: number, l2: number, d2: number, t2: number): boolean {
  if (l1 !== l2) return l1 < l2;
  if (d1 !== d2) return d1 < d2;
  return t1 < t2;
}

export interface PropagateParams {
  w: number;
  h: number;
  heights: Int32Array;
  /** Current tags; recomputed cells are reset, outside cells are read as boundary. */
  tag: Int32Array;
  /** Current distances; same contract as `tag`. */
  dist: Int32Array;
  /** 1 => cell is (re)computed; 0 => frozen boundary cell. null => every cell. */
  region: Uint8Array | null;
  /** Source cells with their tag; sources always keep distance 0. */
  sources: { idx: number; tag: number }[];
  /** Whether the front may propagate from cell `from` into cell `to`. */
  allow: (from: number, to: number) => boolean;
}

/**
 * Deterministic ordered propagation (marker flood).
 *
 * Every recomputed cell adopts the tag of its minimum-key neighbor, where the
 * key of a cell is (height, dist, tag). Its own dist is parent.dist + 1 when
 * the height matches the parent's, otherwise 1. Because the priority queue
 * pops cells in key order, the first neighbor that reaches a cell is always
 * its minimum-key neighbor, so the result is a total function of the inputs:
 * scan order, tile order and insertion order cannot influence the outcome.
 *
 * The class is resumable: `run()` may be called repeatedly with a step
 * budget and reports completion, which lets the engine interrupt and resume
 * an update without ever committing a half-computed label map.
 */
export class Propagation {
  private readonly w: number;
  private readonly h: number;
  private readonly heights: Int32Array;
  private readonly tag: Int32Array;
  private readonly dist: Int32Array;
  private readonly region: Uint8Array | null;
  private readonly allow: (from: number, to: number) => boolean;
  private readonly heap = new MinHeap();
  private readonly hasParent: Uint8Array;
  private readonly pLevel: Int32Array;
  private readonly pDist: Int32Array;
  private readonly pTag: Int32Array;

  constructor(params: PropagateParams, budget?: Budget) {
    const { w, h, heights, tag, dist, region, sources, allow } = params;
    this.w = w;
    this.h = h;
    this.heights = heights;
    this.tag = tag;
    this.dist = dist;
    this.region = region;
    this.allow = allow;
    const n = w * h;
    this.hasParent = new Uint8Array(n);
    this.pLevel = new Int32Array(n);
    this.pDist = new Int32Array(n);
    this.pTag = new Int32Array(n);
    // Reset recomputed cells. Idempotent, so a budget abort here is safe:
    // the job simply re-creates the propagation on resume.
    for (let i = 0; i < n; i++) {
      if (!this.inRegion(i)) continue;
      tag[i] = 0;
      dist[i] = BIG;
      spend(budget);
    }
    // Sources.
    for (const s of sources) {
      tag[s.idx] = s.tag;
      dist[s.idx] = 0;
      this.hasParent[s.idx] = 1;
      this.pLevel[s.idx] = -1;
      this.pDist[s.idx] = -1;
      this.pTag[s.idx] = -1;
      this.heap.push({ level: heights[s.idx], dist: 0, tag: s.tag, idx: s.idx });
      spend(budget);
    }
    // Boundary: recomputed cells adjacent to frozen cells adopt the best
    // (minimum-key) frozen neighbor as their initial parent candidate.
    if (region !== null) {
      for (let i = 0; i < n; i++) {
        if (!this.inRegion(i)) continue;
        spend(budget);
        eachNeighbor(i, w, h, (nb) => {
          if (this.inRegion(nb)) return;
          if (tag[nb] === 0) return;
          if (!this.allow(nb, i)) return;
          this.consider(i, heights[nb], dist[nb], tag[nb]);
        });
      }
    }
  }

  private inRegion(i: number): boolean {
    return this.region === null || this.region[i] === 1;
  }

  private consider(c: number, level: number, dist: number, tag: number): void {
    if (
      this.hasParent[c] === 0 ||
      keyLess(level, dist, tag, this.pLevel[c], this.pDist[c], this.pTag[c])
    ) {
      this.hasParent[c] = 1;
      this.pLevel[c] = level;
      this.pDist[c] = dist;
      this.pTag[c] = tag;
      this.dist[c] = this.heights[c] === level ? dist + 1 : 1;
      this.tag[c] = tag;
      this.heap.push({ level: this.heights[c], dist: this.dist[c], tag, idx: c });
    }
  }

  /** Returns true when the propagation has finished. */
  run(budget?: Budget): boolean {
    while (this.heap.size > 0) {
      spend(budget);
      const e = this.heap.pop() as { level: number; dist: number; tag: number; idx: number };
      if (this.dist[e.idx] !== e.dist || this.tag[e.idx] !== e.tag) continue; // stale entry
      eachNeighbor(e.idx, this.w, this.h, (c) => {
        if (!this.inRegion(c)) return;
        if (!this.allow(e.idx, c)) return;
        this.consider(c, this.heights[e.idx], e.dist, e.tag);
      });
    }
    return true;
  }
}

/**
 * Local fixpoint check for basin tags: a cell is consistent iff it either
 * belongs to a regional-minimum plateau (dist 0, shared tag whose root lies
 * in the plateau) or adopts the tag of its minimum-key neighbor.
 */
export function basinCellConsistent(
  heights: Int32Array,
  w: number,
  h: number,
  basin: Int32Array,
  basinDist: Int32Array,
  basinRoot: Map<number, number>,
  idx: number,
): boolean {
  const plat = plateauInfo(heights, w, h, idx);
  if (plat.minimum) {
    const t = basin[idx];
    if (t < 1 || basinDist[idx] !== 0) return false;
    const root = basinRoot.get(t);
    if (root === undefined || plat.cells.indexOf(root) === -1) return false;
    for (const c of plat.cells) {
      if (basin[c] !== t || basinDist[c] !== 0) return false;
    }
    return true;
  }
  const t0 = basin[idx];
  const d0 = basinDist[idx];
  if (t0 < 1 || d0 < 1 || d0 >= BIG) return false;
  let bl = -1;
  let bd = 0;
  let bt = 0;
  let found = false;
  eachNeighbor(idx, w, h, (n) => {
    if (basin[n] < 1) return;
    if (!found || keyLess(heights[n], basinDist[n], basin[n], bl, bd, bt)) {
      found = true;
      bl = heights[n];
      bd = basinDist[n];
      bt = basin[n];
    }
  });
  if (!found) return false;
  const expectDist = heights[idx] === bl ? bd + 1 : 1;
  return t0 === bt && d0 === expectDist;
}

/**
 * Local fixpoint check for labels: seeds label themselves; cells of seedless
 * basins stay unassigned; every other cell adopts the tag of its minimum-key
 * same-basin neighbor.
 */
export function labelCellConsistent(
  heights: Int32Array,
  w: number,
  h: number,
  basin: Int32Array,
  labels: Int32Array,
  labelDist: Int32Array,
  seedAtCell: (idx: number) => number | undefined,
  basinHasSeed: (basinId: number) => boolean,
  idx: number,
): boolean {
  const seed = seedAtCell(idx);
  if (seed !== undefined) {
    return labels[idx] === seed && labelDist[idx] === 0;
  }
  const b = basin[idx];
  if (!basinHasSeed(b)) {
    return labels[idx] === 0;
  }
  let bl = -1;
  let bd = 0;
  let bt = 0;
  let found = false;
  eachNeighbor(idx, w, h, (n) => {
    if (basin[n] !== b || labels[n] === 0) return;
    if (!found || keyLess(heights[n], labelDist[n], labels[n], bl, bd, bt)) {
      found = true;
      bl = heights[n];
      bd = labelDist[n];
      bt = labels[n];
    }
  });
  if (!found) return labels[idx] === 0;
  const expectDist = heights[idx] === bl ? bd + 1 : 1;
  return labels[idx] === bt && labelDist[idx] === expectDist;
}
