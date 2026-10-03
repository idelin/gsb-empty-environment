/**
 * Core flood machinery shared by the reference oracle and the incremental
 * tiled engine. Everything here is deterministic: the winner of a plateau
 * competition is decided by the global priority key, never by scan order.
 */

export interface Front {
  /** Stable identity, e.g. "seed:A" or "min:137". */
  id: string;
  /** Public label written into the label map. 0 means "unassigned". */
  label: number;
}

/** One priority-queue entry / one relaxed offer to a pixel. */
export interface Entry {
  level: number;
  dist: number;
  front: number; // index into Front[]
  pixel: number;
}

/** Per-pixel flood state (parallel arrays). */
export interface StateMap {
  front: Int32Array; // -1 = not assigned yet
  level: Int32Array;
  dist: Int32Array;
}

export function createStateMap(size: number): StateMap {
  const front = new Int32Array(size).fill(-1);
  return { front, level: new Int32Array(size), dist: new Int32Array(size) };
}

export function cloneStateMap(s: StateMap): StateMap {
  return { front: s.front.slice(), level: s.level.slice(), dist: s.dist.slice() };
}

/**
 * Total order on entries: (level, dist, frontId, pixel).
 * The frontId tie-break makes plateau competition deterministic and
 * independent of scan order, tile order and insertion order.
 */
export function compareEntries(a: Entry, b: Entry, fronts: Front[]): number {
  if (a.level !== b.level) return a.level - b.level;
  if (a.dist !== b.dist) return a.dist - b.dist;
  const fa = fronts[a.front].id;
  const fb = fronts[b.front].id;
  if (fa !== fb) return fa < fb ? -1 : 1;
  return a.pixel - b.pixel;
}

/** Minimal binary heap; entries are plain objects so cursors serialize to JSON. */
export class Heap {
  items: Entry[] = [];
  private fronts: Front[];
  constructor(fronts: Front[]) { this.fronts = fronts; }
  get size(): number { return this.items.length; }
  push(e: Entry): void {
    const items = this.items;
    items.push(e);
    let i = items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (compareEntries(items[i], items[p], this.fronts) < 0) {
        [items[i], items[p]] = [items[p], items[i]];
        i = p;
      } else break;
    }
  }
  pop(): Entry | undefined {
    const items = this.items;
    if (items.length === 0) return undefined;
    const top = items[0];
    const last = items.pop()!;
    if (items.length > 0) {
      items[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1, r = l + 1;
        let m = i;
        if (l < items.length && compareEntries(items[l], items[m], this.fronts) < 0) m = l;
        if (r < items.length && compareEntries(items[r], items[m], this.fronts) < 0) m = r;
        if (m === i) break;
        [items[i], items[m]] = [items[m], items[i]];
        i = m;
      }
    }
    return top;
  }
}

export interface MinimaResult {
  /** Anchor pixel (lowest index) of every regional-minimum plateau. */
  anchors: number[];
  /** minId[pixel] = index into anchors, or -1 when not on a minimum. */
  minId: Int32Array;
}

/** Regional minima = connected equal-height plateaus with no lower neighbor. */
export function computeMinima(width: number, height: number, heights: Int32Array): MinimaResult {
  const n = width * height;
  const minId = new Int32Array(n).fill(-1);
  const visited = new Uint8Array(n);
  const anchors: number[] = [];
  const stack: number[] = [];
  const component: number[] = [];
  for (let start = 0; start < n; start++) {
    if (visited[start] === 1) continue;
    const h = heights[start];
    let isMin = true;
    let anchor = start;
    component.length = 0;
    stack.length = 0;
    stack.push(start);
    visited[start] = 1;
    while (stack.length > 0) {
      const p = stack.pop()!;
      component.push(p);
      if (p < anchor) anchor = p;
      const x = p % width;
      const tryVisit = (q: number): void => {
        const hq = heights[q];
        if (hq < h) isMin = false;
        else if (hq === h && visited[q] === 0) {
          visited[q] = 1;
          stack.push(q);
        }
      };
      if (x > 0) tryVisit(p - 1);
      if (x < width - 1) tryVisit(p + 1);
      if (p >= width) tryVisit(p - width);
      if (p < n - width) tryVisit(p + width);
    }
    if (isMin) {
      const id = anchors.length;
      anchors.push(anchor);
      for (const p of component) minId[p] = id;
    }
  }
  return { anchors, minId };
}

export interface SeedLike { id: string; x: number; y: number; label: number; }

/**
 * Build the source fronts for one segmentation: every seed is a front, and
 * every regional minimum that carries no seed becomes an unassigned
 * (label 0) pseudo-front so seedless basins stay unassigned.
 */
export function buildFronts(
  seeds: SeedLike[],
  minima: MinimaResult,
  width: number,
): { fronts: Front[]; sources: { pixel: number; front: number }[] } {
  const fronts: Front[] = [];
  const sources: { pixel: number; front: number }[] = [];
  const seededMin = new Set<number>();
  const sorted = [...seeds].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
  for (const s of sorted) {
    const pixel = s.y * width + s.x;
    if (minima.minId[pixel] >= 0) seededMin.add(minima.minId[pixel]);
    fronts.push({ id: `seed:${s.id}`, label: s.label });
    sources.push({ pixel, front: fronts.length - 1 });
  }
  for (let i = 0; i < minima.anchors.length; i++) {
    if (seededMin.has(i)) continue;
    fronts.push({ id: `min:${minima.anchors[i]}`, label: 0 });
    sources.push({ pixel: minima.anchors[i], front: fronts.length - 1 });
  }
  return { fronts, sources };
}

/** Relax an offer with state (level, dist) onto a neighbor of height `h`. */
export function relax(level: number, dist: number, h: number): { level: number; dist: number } {
  const nl = Math.max(level, h);
  return { level: nl, dist: nl === level ? dist + 1 : 0 };
}

export interface FloodEnv {
  width: number;
  height: number;
  heights: Int32Array;
  fronts: Front[];
  /** 1 = pixel may be (re)written by this flood, 0 = frozen outside region. */
  region: Uint8Array | null;
  state: StateMap;
}

export interface FloodCursor {
  heap: Heap;
  pops: number;
}

export function createFloodCursor(fronts: Front[]): FloodCursor {
  return { heap: new Heap(fronts), pops: 0 };
}

/**
 * Resumable Dijkstra-style priority flood. Pops at most `budget` pixels
 * (Infinity for a full run). Returns true when the heap is drained.
 * Region pixels must have been reset to front=-1 before the first call.
 */
export function floodStep(env: FloodEnv, cursor: FloodCursor, budget: number): boolean {
  const { width, heights, region, state } = env;
  const n = width * env.height;
  let remaining = budget;
  while (remaining > 0) {
    const e = cursor.heap.pop();
    if (e === undefined) return true;
    if (state.front[e.pixel] !== -1) continue; // already finalized
    state.front[e.pixel] = e.front;
    state.level[e.pixel] = e.level;
    state.dist[e.pixel] = e.dist;
    cursor.pops++;
    remaining--;
    const x = e.pixel % width;
    if (x > 0) offer(e, e.pixel - 1);
    if (x < width - 1) offer(e, e.pixel + 1);
    if (e.pixel >= width) offer(e, e.pixel - width);
    if (e.pixel < n - width) offer(e, e.pixel + width);
  }
  return cursor.heap.size === 0;

  function offer(e: Entry, q: number): void {
    if (region !== null && region[q] === 0) return;
    if (state.front[q] !== -1) return;
    const r = relax(e.level, e.dist, heights[q]);
    cursor.heap.push({ level: r.level, dist: r.dist, front: e.front, pixel: q });
  }
}
