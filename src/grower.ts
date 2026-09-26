import { colorDistance, type RGBA } from "./color.ts";
import { RgbaImage } from "./image.ts";

/**
 * Threshold semantics (the two modes are intentionally separate and must not
 * be mixed):
 *  - "seed":     a pixel is eligible when its color differs from the OWNING
 *                SEED's color by at most `threshold`. Path cost is the max
 *                over pixels on the path of colorDist(pixel, seedColor).
 *  - "neighbor": a pixel is eligible when every step along the path crosses
 *                an edge whose endpoints differ by at most `threshold`.
 *                Path cost is the max edge weight colorDist(u, v).
 */
export type ThresholdMode = "seed" | "neighbor";

export type Connectivity = 4 | 8;

export interface Seed {
  /** Stable id, also used as the final tie-breaker (lexicographic). */
  id: string;
  x: number;
  y: number;
  /** Lower wins conflicts. Defaults to 0. */
  priority?: number;
}

export interface GrowerOptions {
  width: number;
  height: number;
  /** RGBA byte array, length width*height*4. Copied internally. */
  pixels: ArrayLike<number>;
  connectivity: Connectivity;
  threshold: number;
  mode: ThresholdMode;
  /** Optional barrier mask, length width*height; truthy = blocked. */
  barriers?: ArrayLike<number | boolean>;
}

export interface SuppressedSeed {
  id: string;
  x: number;
  y: number;
  reason: string;
}

export interface RegionComponent {
  /** Component label, assigned in scan order. */
  id: number;
  /** Owning seed id. */
  owner: string;
  /** Flat pixel indices (y * width + x). */
  pixels: number[];
}

export interface SourcePath {
  /** Points from the seed to the queried pixel, inclusive. */
  points: { x: number; y: number }[];
  /** Bottleneck cost along this path; equals the pixel's recorded cost. */
  cost: number;
}

export interface GrowResult {
  width: number;
  height: number;
  mode: ThresholdMode;
  connectivity: Connectivity;
  threshold: number;
  /** Per-pixel owning seed id, or null (unreached / over threshold / barrier). */
  owner: (string | null)[];
  /** Per-pixel minimum bottleneck path cost; Infinity when unreachable. */
  cost: number[];
  /** Per-pixel parent pointer (flat index) towards the seed; null at seeds. */
  parent: (number | null)[];
  /** Connected components of equally-owned pixels (splits are separate). */
  components: RegionComponent[];
  /** Seeds dropped because another seed with better priority shared the pixel. */
  suppressedSeeds: SuppressedSeed[];
  ownerAt(x: number, y: number): string | null;
  /** Reconstruct the source path seed -> pixel, or null when unowned. */
  pathTo(x: number, y: number): SourcePath | null;
}

interface ActiveSeed {
  id: string;
  x: number;
  y: number;
  priority: number;
  /** Rank by (priority, id): lower wins every tie. */
  rank: number;
  index: number;
  color: RGBA;
}

/** Iterative binary min-heap keyed by (cost, rank, insertion order). */
class MinHeap {
  private costs: number[] = [];
  private ranks: number[] = [];
  private items: number[] = [];

  get size(): number {
    return this.items.length;
  }

  push(cost: number, rank: number, item: number): void {
    this.costs.push(cost);
    this.ranks.push(rank);
    this.items.push(item);
    let i = this.items.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.less(i, p)) {
        this.swap(i, p);
        i = p;
      } else break;
    }
  }

  pop(): { cost: number; rank: number; item: number } | undefined {
    const n = this.items.length;
    if (n === 0) return undefined;
    const top = { cost: this.costs[0], rank: this.ranks[0], item: this.items[0] };
    this.swap(0, n - 1);
    this.costs.pop();
    this.ranks.pop();
    this.items.pop();
    let i = 0;
    const m = this.items.length;
    for (;;) {
      const l = 2 * i + 1;
      const r = l + 1;
      let s = i;
      if (l < m && this.less(l, s)) s = l;
      if (r < m && this.less(r, s)) s = r;
      if (s === i) break;
      this.swap(i, s);
      i = s;
    }
    return top;
  }

  private less(a: number, b: number): boolean {
    if (this.costs[a] !== this.costs[b]) return this.costs[a] < this.costs[b];
    if (this.ranks[a] !== this.ranks[b]) return this.ranks[a] < this.ranks[b];
    return this.items[a] < this.items[b];
  }

  private swap(a: number, b: number): void {
    [this.costs[a], this.costs[b]] = [this.costs[b], this.costs[a]];
    [this.ranks[a], this.ranks[b]] = [this.ranks[b], this.ranks[a]];
    [this.items[a], this.items[b]] = [this.items[b], this.items[a]];
  }
}

export class RegionGrower {
  readonly image: RgbaImage;
  readonly connectivity: Connectivity;
  threshold: number;
  readonly mode: ThresholdMode;
  private readonly barrierMask: Uint8Array;
  private readonly seeds = new Map<string, Seed>();

  constructor(options: GrowerOptions) {
    this.image = new RgbaImage(options.width, options.height, options.pixels);
    if (options.connectivity !== 4 && options.connectivity !== 8) {
      throw new Error(`connectivity must be 4 or 8, got ${options.connectivity}`);
    }
    this.connectivity = options.connectivity;
    if (!(options.threshold >= 0)) {
      throw new Error(`threshold must be >= 0, got ${options.threshold}`);
    }
    this.threshold = options.threshold;
    if (options.mode !== "seed" && options.mode !== "neighbor") {
      throw new Error(`mode must be "seed" or "neighbor", got ${options.mode}`);
    }
    this.mode = options.mode;
    this.barrierMask = new Uint8Array(options.width * options.height);
    if (options.barriers !== undefined) {
      if (options.barriers.length !== options.width * options.height) {
        throw new Error("barrier mask length mismatch");
      }
      for (let i = 0; i < this.barrierMask.length; i++) {
        this.barrierMask[i] = options.barriers[i] ? 1 : 0;
      }
    }
  }

  isBarrier(x: number, y: number): boolean {
    return this.barrierMask[this.image.index(x, y)] === 1;
  }

  addSeed(seed: Seed): void {
    this.image.index(seed.x, seed.y); // bounds check
    if (this.isBarrier(seed.x, seed.y)) {
      throw new Error(
        `seed "${seed.id}" at (${seed.x}, ${seed.y}) is on a barrier pixel`,
      );
    }
    if (this.seeds.has(seed.id)) {
      throw new Error(`duplicate seed id "${seed.id}"`);
    }
    this.seeds.set(seed.id, { ...seed });
  }

  /** Remove a seed; the next compute() repairs ownership globally. */
  removeSeed(id: string): boolean {
    return this.seeds.delete(id);
  }

  /** Change a pixel's color; the next compute() repairs ownership globally. */
  setPixel(x: number, y: number, rgba: RGBA): void {
    this.image.set(x, y, rgba);
  }

  /** Open or close a barrier pixel; the next compute() repairs ownership. */
  setBarrier(x: number, y: number, on: boolean): void {
    const idx = this.image.index(x, y);
    if (on) {
      for (const s of this.seeds.values()) {
        if (s.x === x && s.y === y) {
          throw new Error(
            `cannot barrier (${x}, ${y}): seed "${s.id}" occupies it`,
          );
        }
      }
    }
    this.barrierMask[idx] = on ? 1 : 0;
  }

  private neighborOffsets(): readonly (readonly [number, number])[] {
    return this.connectivity === 4
      ? [[1, 0], [-1, 0], [0, 1], [0, -1]]
      : [[1, 0], [-1, 0], [0, 1], [0, -1], [1, 1], [1, -1], [-1, 1], [-1, -1]];
  }

  /**
   * Resolve same-pixel seed conflicts: per pixel only the best (priority, id)
   * seed stays active; the rest are reported as suppressed.
   */
  private resolveSeeds(): { active: ActiveSeed[]; suppressed: SuppressedSeed[] } {
    const byPixel = new Map<number, Seed[]>();
    for (const s of this.seeds.values()) {
      const idx = s.y * this.image.width + s.x;
      const list = byPixel.get(idx);
      if (list) list.push(s);
      else byPixel.set(idx, [s]);
    }
    const winners: Seed[] = [];
    const suppressed: SuppressedSeed[] = [];
    for (const [idx, list] of byPixel) {
      list.sort(
        (a, b) =>
          (a.priority ?? 0) - (b.priority ?? 0) ||
          (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
      );
      winners.push(list[0]);
      for (let k = 1; k < list.length; k++) {
        suppressed.push({
          id: list[k].id,
          x: list[k].x,
          y: list[k].y,
          reason: `pixel ${idx} won by seed "${list[0].id}" (priority/id)`,
        });
      }
    }
    winners.sort(
      (a, b) =>
        (a.priority ?? 0) - (b.priority ?? 0) ||
        (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
    );
    const active: ActiveSeed[] = winners.map((s, rank) => ({
      id: s.id,
      x: s.x,
      y: s.y,
      priority: s.priority ?? 0,
      rank,
      index: s.y * this.image.width + s.x,
      color: this.image.get(s.x, s.y),
    }));
    return { active, suppressed };
  }

  /**
   * Full multi-source minimum-bottleneck-path computation.
   *
   * Every mutation (seed removal, pixel change, barrier toggle) is repaired
   * by recomputing ownership over the WHOLE image from ALL current seeds --
   * never by growing only from the changed seed -- so pixels orphaned by a
   * removed seed are correctly re-assigned to competing seeds or released.
   *
   * Iterative (explicit heap + stacks), no recursion: safe for huge plateaus.
   */
  compute(): GrowResult {
    const { width, height } = this.image;
    const n = width * height;
    const { active, suppressed } = this.resolveSeeds();

    const dist = new Float64Array(n).fill(Infinity);
    const ownerRank = new Int32Array(n).fill(-1);
    const ownerId: (string | null)[] = new Array(n).fill(null);
    const parent: (number | null)[] = new Array(n).fill(null);

    const heap = new MinHeap();
    for (const s of active) {
      dist[s.index] = 0;
      ownerRank[s.index] = s.rank;
      ownerId[s.index] = s.id;
      heap.push(0, s.rank, s.index);
    }

    const offsets = this.neighborOffsets();
    const colors: RGBA[] = new Array(n);
    for (let i = 0; i < n; i++) {
      const o = i * 4;
      colors[i] = [
        this.image.data[o],
        this.image.data[o + 1],
        this.image.data[o + 2],
        this.image.data[o + 3],
      ];
    }

    // Multi-source widest-path (minimax) Dijkstra with (cost, rank) keys.
    while (heap.size > 0) {
      const { cost, rank, item } = heap.pop()!;
      // Skip stale heap entries.
      if (cost > dist[item]) continue;
      if (cost === dist[item] && rank !== ownerRank[item]) continue;
      const seed = active[rank];
      const ux = item % width;
      const uy = (item / width) | 0;
      for (const [dx, dy] of offsets) {
        const vx = ux + dx;
        const vy = uy + dy;
        if (vx < 0 || vy < 0 || vx >= width || vy >= height) continue;
        const v = vy * width + vx;
        if (this.barrierMask[v]) continue;
        const step =
          this.mode === "seed"
            ? colorDistance(colors[v], seed.color)
            : colorDistance(colors[item], colors[v]);
        const cand = cost > step ? cost : step; // bottleneck = max along path
        if (
          cand < dist[v] ||
          (cand === dist[v] && (ownerRank[v] === -1 || rank < ownerRank[v]))
        ) {
          dist[v] = cand;
          ownerRank[v] = rank;
          ownerId[v] = seed.id;
          parent[v] = item;
          heap.push(cand, rank, v);
        }
      }
    }

    // Apply the threshold: over-threshold pixels become unowned.
    const finalOwner: (string | null)[] = new Array(n).fill(null);
    const finalParent: (number | null)[] = new Array(n).fill(null);
    for (let i = 0; i < n; i++) {
      if (!this.barrierMask[i] && ownerId[i] !== null && dist[i] <= this.threshold) {
        finalOwner[i] = ownerId[i];
        finalParent[i] = parent[i];
      }
    }

    const components = this.labelComponents(finalOwner);

    const result: GrowResult = {
      width,
      height,
      mode: this.mode,
      connectivity: this.connectivity,
      threshold: this.threshold,
      owner: finalOwner,
      cost: Array.from(dist, (d, i) =>
        finalOwner[i] === null ? Infinity : d,
      ),
      parent: finalParent,
      components,
      suppressedSeeds: suppressed,
      ownerAt(x: number, y: number): string | null {
        return finalOwner[y * width + x];
      },
      pathTo(x: number, y: number): SourcePath | null {
        if (x < 0 || y < 0 || x >= width || y >= height) {
          throw new Error(`pixel (${x}, ${y}) out of bounds`);
        }
        const start = y * width + x;
        if (finalOwner[start] === null) return null;
        const points: { x: number; y: number }[] = [];
        let cur: number | null = start;
        let guard = 0;
        while (cur !== null) {
          if (++guard > n) throw new Error("parent cycle detected");
          points.push({ x: cur % width, y: (cur / width) | 0 });
          cur = finalParent[cur];
        }
        points.reverse();
        return { points, cost: dist[start] };
      },
    };
    return result;
  }

  /** Iterative connected-component labeling over equally-owned pixels. */
  private labelComponents(owner: (string | null)[]): RegionComponent[] {
    const { width, height } = this.image;
    const n = width * height;
    const label = new Int32Array(n).fill(-1);
    const components: RegionComponent[] = [];
    const offsets = this.neighborOffsets();
    const stack: number[] = [];
    for (let s = 0; s < n; s++) {
      if (owner[s] === null || label[s] !== -1) continue;
      const id = components.length;
      const pixels: number[] = [];
      label[s] = id;
      stack.push(s);
      while (stack.length > 0) {
        const u = stack.pop()!;
        pixels.push(u);
        const ux = u % width;
        const uy = (u / width) | 0;
        for (const [dx, dy] of offsets) {
          const vx = ux + dx;
          const vy = uy + dy;
          if (vx < 0 || vy < 0 || vx >= width || vy >= height) continue;
          const v = vy * width + vx;
          if (label[v] === -1 && owner[v] === owner[s]) {
            label[v] = id;
            stack.push(v);
          }
        }
      }
      components.push({ id, owner: owner[s]!, pixels });
    }
    return components;
  }
}
