/**
 * Incremental tiled marker-controlled watershed engine.
 *
 * Semantics (shared with the reference oracle in reference.ts):
 *  - sources are seeds plus pseudo-sources on seedless regional minima, so
 *    basins without a seed stay unassigned (label 0);
 *  - priority key (level, dist, frontId) makes plateau competition a global
 *    deterministic rule: equal level+distance ties are won by the smaller
 *    front id, never by scan order or tile order;
 *  - edits only re-flood the affected tiles; if a plateau carries the effect
 *    across a tile border the dirty region grows until border states match
 *    the committed ones, so the fixpoint always equals the full re-flood;
 *  - commits are atomic: while a budget-limited job is paused, queries keep
 *    seeing the last fully labeled version (never a half-labeled map).
 */
import {
  Heap, buildFronts, cloneStateMap, computeMinima, createFloodCursor, createStateMap,
  floodStep, relax, type FloodCursor, type Front, type StateMap,
} from './core.ts';

export interface SeedRecord { id: string; x: number; y: number; label: number; }
export interface PatchEntry { pixel: number; from: number; to: number; }
export type SegmentEvent =
  | { type: 'merge'; from: number; into: number[] }
  | { type: 'split'; from: number; into: number };
export interface BoundaryEvidence { a: number; b: number; count: number; samples: number[]; }
export interface JobStats { pops: number; regionTiles: number; regionPixels: number; expansions: number; }
export interface VersionRecord {
  version: number;
  patch: PatchEntry[];
  events: SegmentEvent[];
  adjacency: BoundaryEvidence[];
  stats: JobStats;
}
export interface HeightEdit { x: number; y: number; h: number; }
export interface EngineOptions {
  tileSize?: number;
  /** Optional shuffler to randomize tile/pixel processing order (testing). */
  randomize?: (arr: number[]) => void;
}

export class WatershedError extends Error {
  readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'WatershedError';
    this.code = code;
  }
}

interface Job {
  fronts: Front[];
  sources: { pixel: number; front: number }[];
  regionTiles: Set<number>;
  regionMask: Uint8Array;
  work: StateMap;
  cursor: FloodCursor | null;
  stats: JobStats;
}

export class WatershedEngine {
  readonly width: number;
  readonly height: number;
  readonly tileSize: number;
  private readonly tilesX: number;
  private readonly tilesY: number;
  private heights: Int32Array;
  private seeds = new Map<string, SeedRecord>();
  private freeLabels: number[] = [];
  private nextLabel = 1;
  private randomize?: (arr: number[]) => void;

  private fronts: Front[] = [];
  private committed: StateMap;
  private labels: Int32Array;
  private adjacency: BoundaryEvidence[] = [];
  private versions: VersionRecord[] = [];
  private job: Job | null = null;

  constructor(width: number, height: number, heights: Int32Array, opts: EngineOptions = {}) {
    if (width <= 0 || height <= 0) throw new WatershedError('BAD_DIMS', 'width/height must be positive');
    if (heights.length !== width * height) throw new WatershedError('BAD_HEIGHTS', 'heightmap size mismatch');
    this.width = width;
    this.height = height;
    this.tileSize = opts.tileSize ?? 8;
    this.tilesX = Math.ceil(width / this.tileSize);
    this.tilesY = Math.ceil(height / this.tileSize);
    this.randomize = opts.randomize;
    this.heights = heights.slice();
    const n = width * height;
    this.committed = createStateMap(n);
    this.labels = new Int32Array(n);
    const allTiles = new Set<number>();
    for (let t = 0; t < this.tilesX * this.tilesY; t++) allTiles.add(t);
    this.startJob(allTiles);
    this.runJob(Infinity);
  }

  // ---------------------------------------------------------------- queries
  get version(): number { return this.versions.length - 1; }
  get paused(): boolean { return this.job !== null; }
  getSeeds(): SeedRecord[] { return [...this.seeds.values()].map(s => ({ ...s })); }
  getLabel(x: number, y: number): number { return this.labels[y * this.width + x]; }
  getLabels(): Int32Array { return this.labels.slice(); }
  getHeights(): Int32Array { return this.heights.slice(); }
  getAdjacency(version = this.version): BoundaryEvidence[] {
    return structuredClone(this.versions[this.checkVersion(version)].adjacency);
  }
  getPatch(version = this.version): PatchEntry[] {
    return this.versions[this.checkVersion(version)].patch.map(p => ({ ...p }));
  }
  getEvents(version = this.version): SegmentEvent[] {
    return structuredClone(this.versions[this.checkVersion(version)].events);
  }
  getStats(version = this.version): JobStats {
    return { ...this.versions[this.checkVersion(version)].stats };
  }

  /** Reconstruct the full label map of an older committed version. */
  getLabelsVersion(version: number): Int32Array {
    this.checkVersion(version);
    const out = this.labels.slice();
    for (let v = this.versions.length - 1; v > version; v--) {
      for (const e of this.versions[v].patch) out[e.pixel] = e.from;
    }
    return out;
  }

  /** Label of one pixel at an older committed version. */
  getLabelAt(x: number, y: number, version: number): number {
    this.checkVersion(version);
    const pixel = y * this.width + x;
    let label = this.labels[pixel];
    for (let v = this.versions.length - 1; v > version; v--) {
      for (const e of this.versions[v].patch) {
        if (e.pixel === pixel) { label = e.from; break; }
      }
    }
    return label;
  }

  private checkVersion(v: number): number {
    if (!Number.isInteger(v) || v < 0 || v > this.version) {
      throw new WatershedError('BAD_VERSION', `version ${v} out of range 0..${this.version}`);
    }
    return v;
  }

  // ------------------------------------------------------------- mutations
  addSeed(id: string, x: number, y: number, opts?: { budget?: number }): number {
    if (typeof id !== 'string' || id.length === 0) throw new WatershedError('BAD_SEED_ID', 'seed id must be a non-empty string');
    if (this.seeds.has(id)) throw new WatershedError('DUPLICATE_SEED_ID', `seed "${id}" already exists`);
    this.checkCoords(x, y);
    this.checkPositionFree(x, y, null);
    const label = this.freeLabels.length > 0 ? this.freeLabels.pop()! : this.nextLabel++;
    this.mutate(() => {
      this.seeds.set(id, { id, x, y, label });
      return { edited: [y * this.width + x], dirtyFronts: [] };
    }, opts);
    return label;
  }

  moveSeed(id: string, x: number, y: number, opts?: { budget?: number }): void {
    const seed = this.requireSeed(id);
    this.checkCoords(x, y);
    this.checkPositionFree(x, y, id);
    this.mutate(() => {
      seed.x = x;
      seed.y = y;
      // The front id is unchanged, so explicitly re-flood its old region
      // (its territory must be re-competed) plus the landing tile.
      return { edited: [y * this.width + x], dirtyFronts: [`seed:${id}`] };
    }, opts);
  }

  removeSeed(id: string, opts?: { budget?: number }): void {
    const seed = this.requireSeed(id);
    this.mutate(() => {
      this.seeds.delete(id);
      this.freeLabels.push(seed.label);
      return { edited: [], dirtyFronts: [] }; // front diff re-floods its old region
    }, opts);
  }

  setHeights(edits: HeightEdit[], opts?: { budget?: number }): void {
    if (!Array.isArray(edits) || edits.length === 0) throw new WatershedError('BAD_EDITS', 'edits must be a non-empty array');
    for (const e of edits) { // validate everything before touching anything
      this.checkCoords(e.x, e.y);
      if (!Number.isInteger(e.h)) throw new WatershedError('BAD_HEIGHT', `height ${e.h} is not an integer`);
    }
    this.mutate(() => {
      const edited: number[] = [];
      for (const e of edits) {
        const p = e.y * this.width + e.x;
        this.heights[p] = e.h;
        edited.push(p);
      }
      return { edited, dirtyFronts: [] };
    }, opts);
  }

  /** Continue a budget-paused job. Returns true when committed. */
  step(budget = Infinity): boolean {
    if (this.job === null) return true;
    this.runJob(budget);
    return this.job === null;
  }

  private checkCoords(x: number, y: number): void {
    if (!Number.isInteger(x) || !Number.isInteger(y) || x < 0 || y < 0 || x >= this.width || y >= this.height) {
      throw new WatershedError('OUT_OF_BOUNDS', `(${x},${y}) outside ${this.width}x${this.height}`);
    }
  }
  private checkPositionFree(x: number, y: number, exceptId: string | null): void {
    for (const s of this.seeds.values()) {
      if (s.x === x && s.y === y && s.id !== exceptId) {
        throw new WatershedError('SEED_POSITION_CONFLICT', `(${x},${y}) already occupied by seed "${s.id}"`);
      }
    }
  }
  private requireSeed(id: string): SeedRecord {
    const s = this.seeds.get(id);
    if (s === undefined) throw new WatershedError('UNKNOWN_SEED', `seed "${id}" does not exist`);
    return s;
  }

  // ---------------------------------------------------------- job machinery
  private mutate(apply: () => { edited: number[]; dirtyFronts: string[] }, opts?: { budget?: number }): void {
    if (this.job !== null) throw new WatershedError('JOB_ACTIVE', 'a paused job exists; call step() to finish it first');
    const { edited, dirtyFronts } = apply();
    const minima = computeMinima(this.width, this.height, this.heights);
    const { fronts, sources } = buildFronts([...this.seeds.values()], minima, this.width);
    const dirty = new Set<number>();
    for (const p of edited) dirty.add(this.tileOf(p));
    const oldIdxById = new Map(this.fronts.map((f, i) => [f.id, i] as const));
    const newIds = new Set(fronts.map(f => f.id));
    for (const [id, idx] of oldIdxById) {
      if (!newIds.has(id)) this.collectRegionTiles(idx, dirty); // removed front: re-compete its region
    }
    for (const id of dirtyFronts) {
      const idx = oldIdxById.get(id);
      if (idx !== undefined) this.collectRegionTiles(idx, dirty);
    }
    for (const s of sources) {
      if (!oldIdxById.has(fronts[s.front].id)) dirty.add(this.tileOf(s.pixel)); // new front
    }
    // Remap committed states onto the new front indices (by stable front id).
    const work = cloneStateMap(this.committed);
    const newIdxById = new Map(fronts.map((f, i) => [f.id, i] as const));
    for (let p = 0; p < work.front.length; p++) {
      const cf = work.front[p];
      if (cf === -1) continue;
      const ni = newIdxById.get(this.fronts[cf].id);
      work.front[p] = ni === undefined ? -1 : ni;
    }
    const regionMask = new Uint8Array(this.width * this.height);
    for (const t of dirty) for (const p of this.tilePixels(t)) regionMask[p] = 1;
    this.job = {
      fronts, sources, regionTiles: dirty, regionMask, work, cursor: null,
      stats: { pops: 0, regionTiles: 0, regionPixels: 0, expansions: 0 },
    };
    this.runJob(opts?.budget ?? Infinity);
  }

  private startJob(regionTiles: Set<number>): void {
    const minima = computeMinima(this.width, this.height, this.heights);
    const { fronts, sources } = buildFronts([...this.seeds.values()], minima, this.width);
    const regionMask = new Uint8Array(this.width * this.height);
    for (const t of regionTiles) for (const p of this.tilePixels(t)) regionMask[p] = 1;
    this.job = {
      fronts, sources, regionTiles, regionMask,
      work: createStateMap(this.width * this.height), cursor: null,
      stats: { pops: 0, regionTiles: 0, regionPixels: 0, expansions: 0 },
    };
  }

  private runJob(budget: number): void {
    const job = this.job!;
    let remaining = budget;
    for (;;) {
      if (job.cursor === null) this.prepareFlood(job);
      const before = job.cursor!.pops;
      const done = floodStep(this.floodEnv(job), job.cursor!, remaining);
      remaining -= job.cursor!.pops - before;
      if (!done) return; // budget exhausted mid-flood: committed map untouched
      const grown = this.expandRegion(job);
      if (grown === 0) {
        job.stats.pops += job.cursor!.pops;
        this.commit(job);
        this.job = null;
        return;
      }
      job.stats.expansions++;
      job.stats.pops += job.cursor!.pops;
      job.cursor = null; // border states moved: re-flood the grown region
      if (remaining <= 0) return;
    }
  }

  private floodEnv(job: Job) {
    return {
      width: this.width, height: this.height, heights: this.heights,
      fronts: job.fronts, region: job.regionMask, state: job.work,
    };
  }

  /** Reset region pixels and seed the heap with interior sources + imports. */
  private prepareFlood(job: Job): void {
    const cursor = createFloodCursor(job.fronts);
    const pixels: number[] = [];
    for (const t of this.ordered([...job.regionTiles])) {
      for (const p of this.ordered(this.tilePixels(t))) pixels.push(p);
    }
    for (const p of pixels) job.work.front[p] = -1;
    for (const s of job.sources) {
      if (job.regionMask[s.pixel] === 1) {
        cursor.heap.push({ level: this.heights[s.pixel], dist: -1, front: s.front, pixel: s.pixel });
      }
    }
    for (const p of pixels) {
      for (const q of this.neighbors(p)) {
        if (job.regionMask[q] === 1) continue;
        const f = job.work.front[q]; // clean committed state, already remapped
        if (f === -1) continue;
        const r = relax(job.work.level[q], job.work.dist[q], this.heights[p]);
        cursor.heap.push({ level: r.level, dist: r.dist, front: f, pixel: p });
      }
    }
    job.cursor = cursor;
  }

  /**
   * If any border pixel of the region changed its flood state versus the
   * committed map, the effect (e.g. a plateau front) crosses the tile border:
   * absorb the neighboring tiles and re-flood. Returns tiles absorbed.
   */
  private expandRegion(job: Job): number {
    const grow = new Set<number>();
    for (const t of this.ordered([...job.regionTiles])) {
      for (const p of this.tilePixels(t)) {
        let border = false;
        for (const q of this.neighbors(p)) {
          if (job.regionMask[q] === 0) { border = true; break; }
        }
        if (!border) continue;
        if (this.stateMatchesCommitted(job, p)) continue;
        for (const q of this.neighbors(p)) {
          if (job.regionMask[q] === 0) grow.add(this.tileOf(q));
        }
      }
    }
    for (const t of grow) {
      job.regionTiles.add(t);
      for (const p of this.tilePixels(t)) job.regionMask[p] = 1;
    }
    return grow.size;
  }

  private stateMatchesCommitted(job: Job, p: number): boolean {
    const wf = job.work.front[p];
    const cf = this.committed.front[p];
    if (cf === -1) return wf === -1;
    if (wf === -1) return false;
    return job.fronts[wf].id === this.fronts[cf].id
      && job.work.level[p] === this.committed.level[p]
      && job.work.dist[p] === this.committed.dist[p];
  }

  // ---------------------------------------------------------------- commit
  private commit(job: Job): void {
    const n = this.width * this.height;
    const newLabels = new Int32Array(n);
    for (let p = 0; p < n; p++) {
      newLabels[p] = job.work.front[p] === -1 ? 0 : job.fronts[job.work.front[p]].label;
    }
    const patch: PatchEntry[] = [];
    for (let p = 0; p < n; p++) {
      if (newLabels[p] !== this.labels[p]) patch.push({ pixel: p, from: this.labels[p], to: newLabels[p] });
    }
    const events = deriveEvents(this.labels, newLabels);
    this.fronts = job.fronts;
    this.committed = job.work;
    this.labels = newLabels;
    this.adjacency = computeAdjacency(newLabels, this.width, this.height);
    job.stats.regionTiles = job.regionTiles.size;
    let regionPixels = 0;
    for (let p = 0; p < n; p++) if (job.regionMask[p] === 1) regionPixels++;
    job.stats.regionPixels = regionPixels;
    this.versions.push({
      version: this.versions.length, patch, events,
      adjacency: this.adjacency, stats: { ...job.stats },
    });
  }

  // --------------------------------------------------------------- helpers
  private tileOf(pixel: number): number {
    const x = pixel % this.width;
    const y = (pixel / this.width) | 0;
    return ((y / this.tileSize) | 0) * this.tilesX + ((x / this.tileSize) | 0);
  }

  private tilePixels(t: number): number[] {
    const tx = t % this.tilesX;
    const ty = (t / this.tilesX) | 0;
    const out: number[] = [];
    const x0 = tx * this.tileSize, y0 = ty * this.tileSize;
    for (let y = y0; y < Math.min(y0 + this.tileSize, this.height); y++) {
      for (let x = x0; x < Math.min(x0 + this.tileSize, this.width); x++) out.push(y * this.width + x);
    }
    return out;
  }

  private neighbors(p: number): number[] {
    const out: number[] = [];
    const x = p % this.width;
    if (x > 0) out.push(p - 1);
    if (x < this.width - 1) out.push(p + 1);
    if (p >= this.width) out.push(p - this.width);
    if (p < this.width * this.height - this.width) out.push(p + this.width);
    return out;
  }

  private ordered(arr: number[]): number[] {
    if (this.randomize !== undefined) this.randomize(arr);
    return arr;
  }

  /** BFS over the committed region of one front; dirties every tile it touches. */
  private collectRegionTiles(frontIdx: number, dirty: Set<number>): void {
    const n = this.width * this.height;
    const seen = new Uint8Array(n);
    const stack: number[] = [];
    for (let p = 0; p < n; p++) {
      if (this.committed.front[p] === frontIdx) { seen[p] = 1; stack.push(p); }
    }
    while (stack.length > 0) {
      const p = stack.pop()!;
      dirty.add(this.tileOf(p));
      for (const q of this.neighbors(p)) {
        if (seen[q] === 0 && this.committed.front[q] === frontIdx) {
          seen[q] = 1;
          stack.push(q);
        }
      }
    }
  }

  // ---------------------------------------------------------- serialization
  /** Full snapshot including a paused job, so an interrupted run can recover. */
  serialize(): string {
    return JSON.stringify({
      width: this.width, height: this.height, tileSize: this.tileSize,
      heights: Array.from(this.heights),
      seeds: [...this.seeds.values()],
      freeLabels: this.freeLabels, nextLabel: this.nextLabel,
      fronts: this.fronts,
      committed: stateToArrays(this.committed),
      labels: Array.from(this.labels),
      adjacency: this.adjacency,
      versions: this.versions,
      job: this.job === null ? null : {
        fronts: this.job.fronts,
        sources: this.job.sources,
        regionTiles: [...this.job.regionTiles],
        regionMask: Array.from(this.job.regionMask),
        work: stateToArrays(this.job.work),
        cursor: this.job.cursor === null ? null : {
          items: this.job.cursor.heap.items,
          pops: this.job.cursor.pops,
        },
        stats: this.job.stats,
      },
    });
  }

  static deserialize(json: string, opts: EngineOptions = {}): WatershedEngine {
    const d = JSON.parse(json);
    const eng = new WatershedEngine(d.width, d.height, Int32Array.from(d.heights), {
      tileSize: d.tileSize, randomize: opts.randomize,
    });
    eng.heights = Int32Array.from(d.heights);
    eng.seeds = new Map(d.seeds.map((s: SeedRecord) => [s.id, s]));
    eng.freeLabels = d.freeLabels;
    eng.nextLabel = d.nextLabel;
    eng.fronts = d.fronts;
    eng.committed = stateFromArrays(d.committed);
    eng.labels = Int32Array.from(d.labels);
    eng.adjacency = d.adjacency;
    eng.versions = d.versions;
    eng.job = d.job === null ? null : {
      fronts: d.job.fronts,
      sources: d.job.sources,
      regionTiles: new Set(d.job.regionTiles),
      regionMask: Uint8Array.from(d.job.regionMask),
      work: stateFromArrays(d.job.work),
      cursor: d.job.cursor === null ? null : {
        heap: heapFromItems(d.job.cursor.items, d.job.fronts),
        pops: d.job.cursor.pops,
      },
      stats: d.job.stats,
    };
    return eng;
  }
}

function stateToArrays(s: StateMap): { front: number[]; level: number[]; dist: number[] } {
  return { front: Array.from(s.front), level: Array.from(s.level), dist: Array.from(s.dist) };
}
function stateFromArrays(d: { front: number[]; level: number[]; dist: number[] }): StateMap {
  return { front: Int32Array.from(d.front), level: Int32Array.from(d.level), dist: Int32Array.from(d.dist) };
}
function heapFromItems(items: { level: number; dist: number; front: number; pixel: number }[], fronts: Front[]): Heap {
  const h = new Heap(fronts);
  h.items = items;
  return h;
}

// ----------------------------------------------------------- derivations
export function deriveEvents(oldLabels: Int32Array, newLabels: Int32Array): SegmentEvent[] {
  const countOld = new Map<number, number>();
  const countNew = new Map<number, number>();
  const transfers = new Map<string, number>();
  for (let p = 0; p < oldLabels.length; p++) {
    countOld.set(oldLabels[p], (countOld.get(oldLabels[p]) ?? 0) + 1);
    countNew.set(newLabels[p], (countNew.get(newLabels[p]) ?? 0) + 1);
    if (oldLabels[p] !== newLabels[p]) {
      const k = `${oldLabels[p]}->${newLabels[p]}`;
      transfers.set(k, (transfers.get(k) ?? 0) + 1);
    }
  }
  const events: SegmentEvent[] = [];
  for (const [label, before] of [...countOld.entries()].sort((a, b) => a[0] - b[0])) {
    if (before > 0 && (countNew.get(label) ?? 0) === 0) {
      const into = [...transfers.entries()]
        .filter(([k]) => k.startsWith(`${label}->`))
        .sort((a, b) => b[1] - a[1])
        .map(([k]) => Number(k.split('->')[1]));
      events.push({ type: 'merge', from: label, into: [...new Set(into)] });
    }
  }
  for (const [label, after] of [...countNew.entries()].sort((a, b) => a[0] - b[0])) {
    if (after > 0 && (countOld.get(label) ?? 0) === 0) {
      let bestFrom = 0, bestCount = -1;
      for (const [k, c] of transfers) {
        const [from, to] = k.split('->').map(Number);
        if (to === label && c > bestCount) { bestCount = c; bestFrom = from; }
      }
      events.push({ type: 'split', from: bestFrom, into: label });
    }
  }
  return events;
}

export function computeAdjacency(labels: Int32Array, width: number, height: number): BoundaryEvidence[] {
  const map = new Map<string, BoundaryEvidence>();
  const n = width * height;
  for (let p = 0; p < n; p++) {
    const x = p % width;
    if (x < width - 1) edge(p, p + 1);
    if (p < n - width) edge(p, p + width);
  }
  function edge(p: number, q: number): void {
    const a = labels[p], b = labels[q];
    if (a === b) return;
    const lo = Math.min(a, b), hi = Math.max(a, b);
    const key = `${lo}|${hi}`;
    let ev = map.get(key);
    if (ev === undefined) {
      ev = { a: lo, b: hi, count: 0, samples: [] };
      map.set(key, ev);
    }
    ev.count++;
    if (ev.samples.length < 16) ev.samples.push(a === lo ? p : q);
  }
  return [...map.values()].sort((x, y) => x.a - y.a || x.b - y.b);
}
