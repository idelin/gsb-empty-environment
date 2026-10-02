import { cross, dist2, grahamScan } from "./geometry.ts";
import type { Point } from "./geometry.ts";

/** A spatial point identified by `id`, entering the system at a known time. */
export interface TimedPoint extends Point {
  id: string;
}

interface LivePoint extends TimedPoint {
  enteredAt: number;
  expiresAt: number;
}

interface ExpiryEntry {
  expiresAt: number;
  id: string;
}

/** Min-heap keyed by expiry time; stale entries are skipped lazily on pop. */
class ExpiryHeap {
  private items: ExpiryEntry[] = [];

  get size(): number {
    return this.items.length;
  }

  peek(): ExpiryEntry | undefined {
    return this.items[0];
  }

  push(entry: ExpiryEntry): void {
    this.items.push(entry);
    let i = this.items.length - 1;
    while (i > 0) {
      const parent = (i - 1) >> 1;
      if (this.items[parent].expiresAt <= this.items[i].expiresAt) break;
      [this.items[parent], this.items[i]] = [this.items[i], this.items[parent]];
      i = parent;
    }
  }

  pop(): ExpiryEntry | undefined {
    const top = this.items[0];
    const last = this.items.pop();
    if (this.items.length > 0 && last !== undefined) {
      this.items[0] = last;
      let i = 0;
      for (;;) {
        const left = 2 * i + 1;
        const right = left + 1;
        let smallest = i;
        if (
          left < this.items.length &&
          this.items[left].expiresAt < this.items[smallest].expiresAt
        ) {
          smallest = left;
        }
        if (
          right < this.items.length &&
          this.items[right].expiresAt < this.items[smallest].expiresAt
        ) {
          smallest = right;
        }
        if (smallest === i) break;
        [this.items[smallest], this.items[i]] = [
          this.items[i],
          this.items[smallest],
        ];
        i = smallest;
      }
    }
    return top;
  }
}

export interface EvolverStats {
  /** Points merged into the hull with the O(h) incremental path. */
  incrementalInserts: number;
  /** Points that landed inside the current hull and changed nothing. */
  interiorInserts: number;
  /** Full Graham-scan rebuilds, only triggered by hull-vertex expiry/removal. */
  fullRebuilds: number;
  /** Points that expired by TTL. */
  expirations: number;
}

/**
 * Maintains the convex hull of a point set whose members enter with a TTL
 * and expire as time advances.
 *
 * Cost model per time slot:
 *  - insertion: O(h) tangent-chain splice (h = hull vertex count);
 *  - expiry of interior points: O(log n) heap pops, hull untouched;
 *  - expiry of a hull vertex: one O(n log n) rebuild, amortized rare.
 */
export class DynamicConvexHullEvolver {
  private live = new Map<string, LivePoint>();
  private expiry = new ExpiryHeap();
  private hull: LivePoint[] = [];
  private hullIds = new Set<string>();
  private hullDirty = false;
  private now = Number.NEGATIVE_INFINITY;

  readonly stats: EvolverStats = {
    incrementalInserts: 0,
    interiorInserts: 0,
    fullRebuilds: 0,
    expirations: 0,
  };

  /** Current logical time of the evolver. */
  get time(): number {
    return this.now;
  }

  /** Number of currently live (non-expired) points. */
  get size(): number {
    return this.live.size;
  }

  /**
   * Add a point entering at the evolver's current time, expiring after `ttl`.
   * Re-adding an existing id replaces the previous observation.
   */
  addPoint(point: TimedPoint, ttl: number): void {
    if (ttl <= 0) {
      throw new Error(`ttl must be positive, got ${ttl}`);
    }
    if (this.live.has(point.id)) {
      this.removePoint(point.id);
    }
    const livePoint: LivePoint = {
      ...point,
      enteredAt: this.now,
      expiresAt: this.now + ttl,
    };
    this.live.set(point.id, livePoint);
    this.expiry.push({ expiresAt: livePoint.expiresAt, id: point.id });
    this.insertIntoHull(livePoint);
  }

  /** Remove a point immediately (e.g. a drone leaving the swarm). */
  removePoint(id: string): boolean {
    const existing = this.live.get(id);
    if (!existing) return false;
    this.live.delete(id);
    if (this.hullIds.has(id)) {
      this.hullDirty = true;
    }
    return true;
  }

  /**
   * Advance the clock, expiring every point whose TTL has elapsed.
   * Returns the hull of the surviving set.
   */
  advanceTo(time: number): TimedPoint[] {
    if (time < this.now) {
      throw new Error(`time cannot move backwards: ${time} < ${this.now}`);
    }
    this.now = time;
    for (;;) {
      const top = this.expiry.peek();
      if (!top || top.expiresAt > time) break;
      this.expiry.pop();
      const point = this.live.get(top.id);
      // Skip stale heap entries left behind by id replacement.
      if (!point || point.expiresAt !== top.expiresAt) continue;
      this.live.delete(top.id);
      this.stats.expirations++;
      if (this.hullIds.has(top.id)) {
        this.hullDirty = true;
      }
    }
    return this.getHull();
  }

  /** Hull vertices of the live set, in counter-clockwise order. */
  getHull(): TimedPoint[] {
    if (this.hullDirty) {
      this.rebuildHull();
    }
    return this.hull.map((p) => ({ id: p.id, x: p.x, y: p.y }));
  }

  private rebuildHull(): void {
    // grahamScan keeps object references, so vertices stay LivePoints.
    this.setHull(grahamScan([...this.live.values()]) as LivePoint[]);
    this.stats.fullRebuilds++;
  }

  private setHull(vertices: LivePoint[]): void {
    this.hull = vertices;
    this.hullIds = new Set(vertices.map((p) => p.id));
    this.hullDirty = false;
  }

  private insertIntoHull(p: LivePoint): void {
    if (this.hullDirty || this.hull.length < 3) {
      // Degenerate hull: a rebuild over the tiny live set is cheapest and
      // keeps the invariant that `hull` is always exact.
      this.rebuildHull();
      return;
    }
    const n = this.hull.length;
    const visible: boolean[] = new Array(n);
    let anyVisible = false;
    for (let i = 0; i < n; i++) {
      visible[i] = cross(this.hull[i], this.hull[(i + 1) % n], p) < 0;
      anyVisible = anyVisible || visible[i];
    }
    if (!anyVisible) {
      this.stats.interiorInserts++;
      return;
    }
    // Visible edges form one contiguous chain [start..end]; the vertices
    // strictly between them are replaced by the new point.
    let start = 0;
    while (!(visible[start] && !visible[(start - 1 + n) % n])) start++;
    let end = start;
    while (visible[(end + 1) % n]) end = (end + 1) % n;
    const next: LivePoint[] = [];
    let i = (end + 1) % n;
    for (;;) {
      next.push(this.hull[i]);
      if (i === start) break;
      i = (i + 1) % n;
    }
    next.push(p);
    this.setHull(this.tighten(next));
    this.stats.incrementalInserts++;
  }

  /**
   * Remove reflex and collinear-redundant vertices from a circular chain,
   * yielding a strictly convex CCW polygon (or a degenerate 1/2-gon).
   */
  private tighten(chain: LivePoint[]): LivePoint[] {
    const result = [...chain];
    let changed = true;
    while (changed && result.length > 2) {
      changed = false;
      for (let i = 0; i < result.length; i++) {
        const prev = result[(i - 1 + result.length) % result.length];
        const cur = result[i];
        const next = result[(i + 1) % result.length];
        const turn = cross(prev, cur, next);
        if (turn > 0) continue;
        if (turn < 0) {
          result.splice(i, 1);
        } else {
          // Collinear triple: drop the vertex lying between the other two.
          const drop =
            dist2(prev, cur) + dist2(cur, next) <= dist2(prev, next) + 1e-12
              ? i
              : dist2(prev, next) + dist2(next, cur) <= dist2(prev, cur) + 1e-12
                ? (i + 1) % result.length
                : (i - 1 + result.length) % result.length;
          result.splice(drop, 1);
        }
        changed = true;
        break;
      }
    }
    return result;
  }
}
