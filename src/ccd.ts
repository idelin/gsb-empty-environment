import { Rat } from "./rational.ts";
import { aabbOverlap, sweptAabb } from "./geometry.ts";
import type { Aabb, Body } from "./geometry.ts";

export interface ContactInterval {
  readonly pair: readonly [string, string];
  readonly t0: Rat;
  readonly t1: Rat;
}

export interface QueryStats {
  readonly bodies: number;
  readonly cells: number;
  /** Unordered pairs sharing at least one hash cell (after dedupe). */
  readonly cellPairs: number;
  /** Candidates whose swept AABBs also overlap (narrow-phase input). */
  readonly candidates: number;
  /** Pairs checked by the independent all-pairs exact pass. */
  readonly exactPairs: number;
  readonly collisions: number;
  readonly verified: boolean;
  readonly missed: readonly string[];
}

export interface QueryResult {
  readonly collisions: readonly ContactInterval[];
  readonly stats: QueryStats;
}

/**
 * Exact contact interval of two bodies over t in [0, T].
 *
 * Per axis, closed-overlap of the moving intervals gives two linear
 * inequalities in t:
 *   loA + vA*t <= hiB + vB*t   =>   (loA - hiB) + (vA - vB)*t <= 0
 *   loB + vB*t <= hiA + vA*t   =>   (loB - hiA) + (vB - vA)*t <= 0
 * Each inequality a + b*t <= 0 is a closed half-line (or all/empty when
 * b = 0). Intersecting all half-lines with [0, T] yields the exact
 * contact interval [t0, t1]; a single instant (t0 = t1) still counts.
 */
export function contactInterval(a: Body, b: Body, T: Rat): [Rat, Rat] | null {
  let lo = Rat.ZERO;
  let hi = T;

  const axes: readonly [Rat, Rat, Rat, Rat, Rat, Rat][] = [
    [a.rect.x, a.rect.x.add(a.rect.w), a.vx, b.rect.x, b.rect.x.add(b.rect.w), b.vx],
    [a.rect.y, a.rect.y.add(a.rect.h), a.vy, b.rect.y, b.rect.y.add(b.rect.h), b.vy],
  ];

  for (const [loA, hiA, vA, loB, hiB, vB] of axes) {
    const bounds: readonly [Rat, Rat][] = [
      [loA.sub(hiB), vA.sub(vB)],
      [loB.sub(hiA), vB.sub(vA)],
    ];
    for (const [c, d] of bounds) {
      // c + d*t <= 0
      if (d.isZero) {
        if (c.sign() > 0) return null; // never overlaps on this axis
        continue; // always satisfied on this axis
      }
      const tBound = c.neg().div(d); // -c / d
      if (d.sign() > 0) {
        if (tBound.lt(hi)) hi = tBound; // t <= -c/d
      } else {
        if (tBound.gt(lo)) lo = tBound; // t >= -c/d
      }
      if (lo.gt(hi)) return null;
    }
  }
  return lo.lte(hi) ? [lo, hi] : null;
}

export function pairKey(idA: string, idB: string): string {
  return idA < idB ? `${idA} ${idB}` : `${idB} ${idA}`;
}

function checkId(id: string): void {
  if (id.length === 0 || id.includes(" ")) {
    throw new Error(`World: illegal id ${JSON.stringify(id)}`);
  }
}

interface SpatialIndex {
  readonly cells: Map<string, string[]>;
  readonly swept: Map<string, Aabb>;
}

function buildSpatialIndex(bodies: readonly Body[], T: Rat, cellSize: Rat): SpatialIndex {
  const cells = new Map<string, string[]>();
  const swept = new Map<string, Aabb>();
  for (const body of bodies) {
    const box = sweptAabb(body, T);
    swept.set(body.id, box);
    const cx0 = box.minX.floorDiv(cellSize);
    const cx1 = box.maxX.floorDiv(cellSize);
    const cy0 = box.minY.floorDiv(cellSize);
    const cy1 = box.maxY.floorDiv(cellSize);
    for (let cx = cx0; cx <= cx1; cx++) {
      for (let cy = cy0; cy <= cy1; cy++) {
        const key = `${cx},${cy}`;
        let bucket = cells.get(key);
        if (bucket === undefined) {
          bucket = [];
          cells.set(key, bucket);
        }
        bucket.push(body.id);
      }
    }
  }
  return { cells, swept };
}

export interface CandidateSet {
  /** Unique unordered pairs sharing a cell AND overlapping swept AABBs. */
  readonly pairs: readonly (readonly [string, string])[];
  readonly cellPairs: number;
  readonly cells: number;
}

/**
 * Broad phase: spatial hash over swept AABBs. A body touching a cell
 * boundary is inserted into every cell it touches (closed boxes), so
 * pairs may be generated multiple times; they are deduped here.
 */
export function broadPhase(index: SpatialIndex): CandidateSet {
  const seen = new Set<string>();
  let cellPairs = 0;
  for (const bucket of index.cells.values()) {
    for (let i = 0; i < bucket.length; i++) {
      for (let j = i + 1; j < bucket.length; j++) {
        cellPairs++;
        seen.add(pairKey(bucket[i], bucket[j]));
      }
    }
  }
  const pairs: [string, string][] = [];
  for (const key of seen) {
    const [idA, idB] = key.split(" ");
    const boxA = index.swept.get(idA);
    const boxB = index.swept.get(idB);
    if (boxA !== undefined && boxB !== undefined && aabbOverlap(boxA, boxB)) {
      pairs.push(idA < idB ? [idA, idB] : [idB, idA]);
    }
  }
  pairs.sort((p, q) => (p[0] === q[0] ? compareStr(p[1], q[1]) : compareStr(p[0], q[0])));
  return { pairs, cellPairs, cells: index.cells.size };
}

function compareStr(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

/** Independent O(n^2) exact pass over every pair — the ground truth. */
export function allPairsExact(
  bodies: readonly Body[],
  T: Rat,
): Map<string, ContactInterval> {
  const out = new Map<string, ContactInterval>();
  for (let i = 0; i < bodies.length; i++) {
    for (let j = i + 1; j < bodies.length; j++) {
      const a = bodies[i];
      const b = bodies[j];
      const interval = contactInterval(a, b, T);
      if (interval !== null) {
        const pair: [string, string] = a.id < b.id ? [a.id, b.id] : [b.id, a.id];
        out.set(pairKey(a.id, b.id), { pair, t0: interval[0], t1: interval[1] });
      }
    }
  }
  return out;
}

export interface BodySpec {
  readonly id: string | number;
  readonly rect: { x: Rat | string | number; y: Rat | string | number; w: Rat | string | number; h: Rat | string | number };
  readonly v?: readonly [Rat | string | number, Rat | string | number];
}

export interface BatchOp {
  readonly add?: readonly BodySpec[];
  readonly remove?: readonly (string | number)[];
  readonly update?: readonly { id: string | number; rect?: BodySpec["rect"]; v?: readonly [Rat | string | number, Rat | string | number] }[];
}

export interface WorldOptions {
  readonly T: Rat | string | number;
  readonly cellSize?: Rat | string | number;
}

/**
 * Collision world. Mutations only take effect through applyBatch();
 * the spatial index is rebuilt from the live set after each batch, so
 * a removed id can never resurrect from a stale hash cell.
 */
export class World {
  readonly T: Rat;
  readonly cellSize: Rat;
  private readonly bodies = new Map<string, Body>();
  private index: SpatialIndex = { cells: new Map(), swept: new Map() };
  private batchCount = 0;

  constructor(options: WorldOptions) {
    this.T = Rat.of(options.T);
    if (this.T.sign() < 0) throw new Error("World: T must be >= 0");
    this.cellSize = options.cellSize === undefined ? Rat.ONE : Rat.of(options.cellSize);
    if (this.cellSize.sign() <= 0) throw new Error("World: cellSize must be > 0");
  }

  get size(): number {
    return this.bodies.size;
  }

  has(id: string | number): boolean {
    return this.bodies.has(String(id));
  }

  getBody(id: string | number): Body | undefined {
    return this.bodies.get(String(id));
  }

  /** Applies removals, updates and additions atomically, then reindexes. */
  applyBatch(op: BatchOp): void {
    for (const id of op.remove ?? []) {
      this.bodies.delete(String(id));
    }
    for (const u of op.update ?? []) {
      const id = String(u.id);
      checkId(id);
      const prev = this.bodies.get(id);
      if (prev === undefined) throw new Error(`World: update of unknown id "${id}"`);
      this.bodies.set(id, {
        id,
        rect: u.rect === undefined ? prev.rect : parseRect(u.rect),
        vx: u.v === undefined ? prev.vx : Rat.of(u.v[0]),
        vy: u.v === undefined ? prev.vy : Rat.of(u.v[1]),
      });
    }
    for (const spec of op.add ?? []) {
      const id = String(spec.id);
      checkId(id);
      if (this.bodies.has(id)) {
        throw new Error(`World: duplicate id "${id}" (remove it in an earlier batch first)`);
      }
      const v = spec.v ?? [0, 0];
      this.bodies.set(id, {
        id,
        rect: parseRect(spec.rect),
        vx: Rat.of(v[0]),
        vy: Rat.of(v[1]),
      });
    }
    this.index = buildSpatialIndex([...this.bodies.values()], this.T, this.cellSize);
    this.batchCount++;
  }

  get batches(): number {
    return this.batchCount;
  }

  /**
   * Broad phase (spatial hash + swept AABB) feeds the exact narrow
   * phase; an independent all-pairs exact pass verifies no collision
   * was missed by the candidates.
   */
  query(options?: { verify?: boolean }): QueryResult {
    const verify = options?.verify ?? true;
    const bodies = [...this.bodies.values()];
    const byId = this.bodies;

    const candidateSet = broadPhase(this.index);
    const collisions: ContactInterval[] = [];
    for (const [idA, idB] of candidateSet.pairs) {
      const a = byId.get(idA);
      const b = byId.get(idB);
      if (a === undefined || b === undefined) continue; // paranoia: stale cell
      const interval = contactInterval(a, b, this.T);
      if (interval !== null) {
        collisions.push({ pair: [idA, idB], t0: interval[0], t1: interval[1] });
      }
    }

    let exactPairs = 0;
    let verified = true;
    const missed: string[] = [];
    if (verify) {
      const truth = allPairsExact(bodies, this.T);
      exactPairs = (bodies.length * (bodies.length - 1)) / 2;
      const found = new Map(collisions.map((c) => [pairKey(c.pair[0], c.pair[1]), c]));
      if (found.size !== truth.size) verified = false;
      for (const [key, expected] of truth) {
        const got = found.get(key);
        if (got === undefined) {
          verified = false;
          missed.push(key);
        } else if (!got.t0.eq(expected.t0) || !got.t1.eq(expected.t1)) {
          verified = false;
          missed.push(`${key} (interval mismatch)`);
        }
      }
    }

    return {
      collisions,
      stats: {
        bodies: bodies.length,
        cells: candidateSet.cells,
        cellPairs: candidateSet.cellPairs,
        candidates: candidateSet.pairs.length,
        exactPairs,
        collisions: collisions.length,
        verified,
        missed,
      },
    };
  }
}

function parseRect(spec: BodySpec["rect"]): { x: Rat; y: Rat; w: Rat; h: Rat } {
  const rect = { x: Rat.of(spec.x), y: Rat.of(spec.y), w: Rat.of(spec.w), h: Rat.of(spec.h) };
  if (rect.w.sign() < 0 || rect.h.sign() < 0) throw new Error("World: negative rect size");
  return rect;
}
