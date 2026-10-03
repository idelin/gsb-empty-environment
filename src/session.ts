/**
 * Incremental edit session with atomic commit / cancel.
 *
 * A session holds a private working copy of the chunk table (copy-on-write
 * over the base version). Obstacle edits only touch the working obstacle
 * bitmap; the distance field is repaired lazily at commit time using a
 * dynamic raise/lower wavefront (the "dynamic brushfire" scheme):
 *
 *  - RAISE: when an obstacle disappears, every cell whose nearest source
 *    was that obstacle is reset to Infinity. The wave propagates through
 *    the whole Voronoi region of the removed source -- NOT just its direct
 *    neighbors -- by following matching source labels.
 *  - LOWER: boundary cells with still-valid sources plus newly added
 *    obstacles re-propagate exact squared distances (computed from source
 *    coordinates, never accumulated) until the field is consistent again.
 *
 * Nothing is visible to readers until `commit()` publishes a new version
 * in a single atomic swap. `cancel()` drops the working copy, leaving the
 * published version untouched.
 */

import {
  CHUNK_SHIFT,
  CHUNK_SIZE,
  Chunk,
  VersionedField,
  chunkKey,
} from "./field.ts";
import type { Version } from "./field.ts";
import { computeEDT, INF, betterSource } from "./edt.ts";

const NEIGHBORS8: ReadonlyArray<readonly [number, number]> = [
  [-1, -1], [0, -1], [1, -1],
  [-1, 0],           [1, 0],
  [-1, 1],  [0, 1],  [1, 1],
];

interface RaiseEntry { x: number; y: number; sx: number; sy: number }
interface LowerEntry { x: number; y: number; d: number; sx: number; sy: number }

class MinHeap<T> {
  private items: T[] = [];
  private less: (a: T, b: T) => boolean;
  constructor(less: (a: T, b: T) => boolean) {
    this.less = less;
  }
  get size(): number { return this.items.length; }
  push(item: T): void {
    const a = this.items;
    a.push(item);
    let i = a.length - 1;
    while (i > 0) {
      const p = (i - 1) >> 1;
      if (this.less(a[i], a[p])) {
        [a[i], a[p]] = [a[p], a[i]];
        i = p;
      } else break;
    }
  }
  pop(): T | undefined {
    const a = this.items;
    if (a.length === 0) return undefined;
    const top = a[0];
    const last = a.pop()!;
    if (a.length > 0) {
      a[0] = last;
      let i = 0;
      for (;;) {
        const l = 2 * i + 1, r = l + 1;
        let m = i;
        if (l < a.length && this.less(a[l], a[m])) m = l;
        if (r < a.length && this.less(a[r], a[m])) m = r;
        if (m === i) break;
        [a[i], a[m]] = [a[m], a[i]];
        i = m;
      }
    }
    return top;
  }
}

const lowerLess = (a: LowerEntry, b: LowerEntry): boolean =>
  a.d !== b.d ? a.d < b.d
  : a.sx !== b.sx ? a.sx < b.sx
  : a.sy !== b.sy ? a.sy < b.sy
  : a.x !== b.x ? a.x < b.x
  : a.y < b.y;

export class EditSession {
  private field: VersionedField;
  private base: Version;
  private chunks: Map<number, Chunk>;
  private cloned = new Set<number>();
  private pending = new Map<number, boolean>(); // local index -> desired obstacle
  private finished = false;

  constructor(field: VersionedField) {
    this.field = field;
    this.base = field.retain(field.snapshot());
    this.chunks = new Map(this.base.chunks);
  }

  // ---- coordinate helpers (world <-> local) ---------------------------

  private toLocal(wx: number, wy: number): [number, number] {
    return [wx - this.field.originX, wy - this.field.originY];
  }

  private inBounds(lx: number, ly: number): boolean {
    return lx >= 0 && ly >= 0 && lx < this.field.width && ly < this.field.height;
  }

  // ---- chunk / cell access on the working copy -------------------------

  private chunkAt(lx: number, ly: number): Chunk | undefined {
    return this.chunks.get(chunkKey(lx >> CHUNK_SHIFT, ly >> CHUNK_SHIFT));
  }

  private ensureChunk(lx: number, ly: number): Chunk {
    const key = chunkKey(lx >> CHUNK_SHIFT, ly >> CHUNK_SHIFT);
    let c = this.chunks.get(key);
    if (!c) {
      c = new Chunk(CHUNK_SIZE);
      this.chunks.set(key, c);
      this.cloned.add(key);
    } else if (!this.cloned.has(key)) {
      c = c.clone();
      this.chunks.set(key, c);
      this.cloned.add(key);
    }
    return c;
  }

  private cellIndex(lx: number, ly: number): number {
    return (ly & (CHUNK_SIZE - 1)) * CHUNK_SIZE + (lx & (CHUNK_SIZE - 1));
  }

  private getState(lx: number, ly: number): { d: number; sx: number; sy: number } {
    const c = this.chunkAt(lx, ly);
    if (!c) return { d: INF, sx: -1, sy: -1 };
    const i = this.cellIndex(lx, ly);
    return { d: c.dist[i], sx: c.srcX[i], sy: c.srcY[i] };
  }

  private setState(lx: number, ly: number, d: number, sx: number, sy: number): void {
    const c = this.ensureChunk(lx, ly);
    const i = this.cellIndex(lx, ly);
    c.dist[i] = d;
    c.srcX[i] = sx;
    c.srcY[i] = sy;
  }

  private getObstacle(lx: number, ly: number): boolean {
    const c = this.chunkAt(lx, ly);
    return c ? c.obstacle[this.cellIndex(lx, ly)] === 1 : false;
  }

  private setObstacleRaw(lx: number, ly: number, present: boolean): void {
    const c = this.ensureChunk(lx, ly);
    c.obstacle[this.cellIndex(lx, ly)] = present ? 1 : 0;
  }

  // ---- public edit API (world coordinates) ------------------------------

  /** Add or remove a single obstacle cell. Out-of-bounds edits are ignored. */
  setObstacle(wx: number, wy: number, present: boolean): void {
    this.assertActive();
    const [lx, ly] = this.toLocal(wx, wy);
    if (!this.inBounds(lx, ly)) return;
    if (this.getObstacle(lx, ly) === present) return;
    this.setObstacleRaw(lx, ly, present);
    const key = ly * this.field.width + lx;
    const original = this.baseCellObstacle(wx, wy);
    if (present === original) this.pending.delete(key);
    else this.pending.set(key, present);
  }

  /** Translate every obstacle inside a rectangle by (dx, dy). */
  moveRegion(wx: number, wy: number, w: number, h: number, dx: number, dy: number): void {
    this.assertActive();
    const moved: Array<[number, number]> = [];
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        if (this.getObstacle(...this.toLocal(wx + x, wy + y))) {
          moved.push([wx + x, wy + y]);
        }
      }
    }
    for (const [x, y] of moved) this.setObstacle(x, y, false);
    for (const [x, y] of moved) this.setObstacle(x + dx, y + dy, true);
  }

  private baseCellObstacle(wx: number, wy: number): boolean {
    return this.field.getCell(this.base, wx, wy).obstacle;
  }

  // ---- commit / cancel ---------------------------------------------------

  /**
   * Repair the field incrementally and publish atomically.
   * Returns the new consistent version.
   */
  commit(): Version {
    this.assertActive();
    this.finished = true;

    const raiseQueue: RaiseEntry[] = [];
    const lowerHeap = new MinHeap<LowerEntry>(lowerLess);

    // 1. Seed the raise wave from removed obstacles. Their (still
    //    consistent) stored source is themselves.
    for (const [key, present] of this.pending) {
      if (present) continue;
      const lx = key % this.field.width;
      const ly = (key - lx) / this.field.width;
      raiseQueue.push({ x: lx, y: ly, sx: lx, sy: ly });
    }

    // 2. Raise: wipe every cell whose source was removed. The wave follows
    //    source labels through the whole Voronoi region, so far-away cells
    //    (not just direct neighbors of the deleted obstacle) are repaired.
    for (let head = 0; head < raiseQueue.length; head++) {
      const e = raiseQueue[head];
      const st = this.getState(e.x, e.y);
      if (st.sx !== e.sx || st.sy !== e.sy) continue; // already reset
      this.setState(e.x, e.y, INF, -1, -1);
      for (const [dx, dy] of NEIGHBORS8) {
        const nx = e.x + dx, ny = e.y + dy;
        if (!this.inBounds(nx, ny)) continue;
        const nst = this.getState(nx, ny);
        if (nst.sx === e.sx && nst.sy === e.sy) {
          raiseQueue.push({ x: nx, y: ny, sx: e.sx, sy: e.sy });
        } else if (nst.sx >= 0) {
          lowerHeap.push({ x: nx, y: ny, d: nst.d, sx: nst.sx, sy: nst.sy });
        }
      }
    }

    // 3. Seed the lower wave from added obstacles.
    for (const [key, present] of this.pending) {
      if (!present) continue;
      const lx = key % this.field.width;
      const ly = (key - lx) / this.field.width;
      this.setState(lx, ly, 0, lx, ly);
      lowerHeap.push({ x: lx, y: ly, d: 0, sx: lx, sy: ly });
    }

    // 4. Lower: re-propagate exact squared distances from valid sources.
    for (;;) {
      const e = lowerHeap.pop();
      if (!e) break;
      const st = this.getState(e.x, e.y);
      if (st.d !== e.d || st.sx !== e.sx || st.sy !== e.sy) continue; // stale
      for (const [dx, dy] of NEIGHBORS8) {
        const nx = e.x + dx, ny = e.y + dy;
        if (!this.inBounds(nx, ny)) continue;
        const ddx = nx - e.sx, ddy = ny - e.sy;
        const nd = ddx * ddx + ddy * ddy;
        const nst = this.getState(nx, ny);
        if (betterSource(nd, e.sx, e.sy, nst.d, nst.sx, nst.sy)) {
          this.setState(nx, ny, nd, e.sx, e.sy);
          lowerHeap.push({ x: nx, y: ny, d: nd, sx: e.sx, sy: e.sy });
        }
      }
    }

    return this.publishAndRelease();
  }

  /**
   * Recompute the whole field exactly from the working obstacle bitmap
   * (Felzenszwalb-Huttenlocher) and publish atomically.
   */
  commitFull(): Version {
    this.assertActive();
    this.finished = true;
    const { width: w, height: h } = this.field;
    const result = computeEDT({
      width: w,
      height: h,
      obstacle: (x, y) => this.getObstacle(x, y),
    });
    const chunksX = Math.ceil(w / CHUNK_SIZE);
    const chunksY = Math.ceil(h / CHUNK_SIZE);
    for (let cy = 0; cy < chunksY; cy++) {
      for (let cx = 0; cx < chunksX; cx++) {
        const chunk = this.ensureChunk(cx * CHUNK_SIZE, cy * CHUNK_SIZE);
        chunk.dist.fill(INF);
        chunk.srcX.fill(-1);
        chunk.srcY.fill(-1);
        for (let y = 0; y < CHUNK_SIZE; y++) {
          const ly = cy * CHUNK_SIZE + y;
          if (ly >= h) break;
          for (let x = 0; x < CHUNK_SIZE; x++) {
            const lx = cx * CHUNK_SIZE + x;
            if (lx >= w) break;
            const gi = ly * w + lx;
            const ci = y * CHUNK_SIZE + x;
            chunk.dist[ci] = result.dist[gi];
            chunk.srcX[ci] = result.srcX[gi];
            chunk.srcY[ci] = result.srcY[gi];
          }
        }
      }
    }
    return this.publishAndRelease();
  }

  /** Roll back: discard every pending edit; nothing was ever published. */
  cancel(): void {
    this.assertActive();
    this.finished = true;
    this.chunks.clear();
    this.cloned.clear();
    this.pending.clear();
    this.field.release(this.base);
  }

  private publishAndRelease(): Version {
    const v = this.field.publish(this.chunks);
    this.field.release(this.base);
    this.chunks.clear();
    this.cloned.clear();
    this.pending.clear();
    return v;
  }

  private assertActive(): void {
    if (this.finished) throw new Error("edit session is already closed");
  }
}
