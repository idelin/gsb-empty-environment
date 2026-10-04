import { Rational, pointKey } from './rational.ts';
import type { RationalPoint } from './rational.ts';

/**
 * Grid input: either an array of rows (grid[y][x]) of integer sample values,
 * or a flat buffer with explicit dimensions.
 */
export type GridInput =
  | ReadonlyArray<ReadonlyArray<number | bigint>>
  | { data: ArrayLike<number | bigint>; width: number; height: number };

/** A single contour segment with exact rational endpoints. */
export interface Segment {
  readonly a: RationalPoint;
  readonly b: RationalPoint;
  readonly aKey: string;
  readonly bKey: string;
}

// Cell corner layout:          Cell edge indices:
//   a (x, y)   ---- b (x+1, y)        --- 0(top) ---
//   |                |               3(left)     1(right)
//   d (x, y+1) ---- c (x+1, y+1)      --- 2(bottom) --
//
// Case index bit 0 = a, bit 1 = b, bit 2 = c, bit 3 = d (set = inside).
// A vertex is "inside" iff value > threshold (strict). Vertices exactly on
// the threshold are therefore outside, but edges adjacent to them can still
// produce crossings with t = 0/1 landing exactly on the vertex.

type EdgePair = readonly [number, number];

const TOP = 0;
const RIGHT = 1;
const BOTTOM = 2;
const LEFT = 3;

const SADDLE = -1;

/**
 * Segment templates per case. Entry is either a list of edge pairs or SADDLE
 * for the two ambiguous cases (5 and 10).
 */
const CASE_TABLE: ReadonlyArray<ReadonlyArray<EdgePair> | typeof SADDLE> = [
  [], // 0
  [[LEFT, TOP]], // 1: a
  [[TOP, RIGHT]], // 2: b
  [[LEFT, RIGHT]], // 3: a b
  [[RIGHT, BOTTOM]], // 4: c
  SADDLE, // 5: a c
  [[TOP, BOTTOM]], // 6: b c
  [[LEFT, BOTTOM]], // 7: a b c
  [[BOTTOM, LEFT]], // 8: d
  [[TOP, BOTTOM]], // 9: a d
  SADDLE, // 10: b d
  [[RIGHT, BOTTOM]], // 11: a b d
  [[LEFT, RIGHT]], // 12: c d
  [[TOP, RIGHT]], // 13: a c d
  [[LEFT, TOP]], // 14: b c d
  [], // 15
];

/** Segments wrapping corners b and d (connects along the a-c diagonal). */
const SADDLE_ALONG_AC: ReadonlyArray<EdgePair> = [
  [TOP, RIGHT],
  [BOTTOM, LEFT],
];
/** Segments wrapping corners a and c (connects along the b-d diagonal). */
const SADDLE_ALONG_BD: ReadonlyArray<EdgePair> = [
  [LEFT, TOP],
  [RIGHT, BOTTOM],
];

export class ContourExtractor {
  readonly width: number;
  readonly height: number;
  readonly threshold: bigint;

  private readonly valueAt: (x: number, y: number) => bigint;

  /**
   * Memoized edge crossings, keyed by edge identity ("h:x:y" / "v:x:y").
   * Guarantees a crossing on an edge shared by two cells is computed once.
   */
  private readonly crossingCache = new Map<string, RationalPoint | null>();

  /** Number of times an edge crossing was actually interpolated. */
  interpolationCount = 0;

  constructor(grid: GridInput, threshold: number | bigint) {
    if (Array.isArray(grid)) {
      const rows = grid as ReadonlyArray<ReadonlyArray<number | bigint>>;
      this.height = rows.length;
      this.width = this.height === 0 ? 0 : rows[0].length;
      for (const row of rows) {
        if (row.length !== this.width) {
          throw new Error('All grid rows must have the same length');
        }
      }
      this.valueAt = (x, y) => BigInt(rows[y][x]);
    } else {
      this.width = grid.width;
      this.height = grid.height;
      this.valueAt = (x, y) => BigInt(grid.data[y * grid.width + x]);
    }
    if (!Number.isInteger(this.width) || !Number.isInteger(this.height)) {
      throw new Error('Grid dimensions must be integers');
    }
    this.threshold =
      typeof threshold === 'number' ? BigInt(threshold) : threshold;
  }

  private isInside(value: bigint): boolean {
    return value > this.threshold;
  }

  /**
   * Returns the crossing point on the given edge, or null if the edge is not
   * crossed. Horizontal edge (x, y) joins (x, y)-(x+1, y); vertical edge
   * (x, y) joins (x, y)-(x, y+1). Results are memoized per edge.
   */
  edgeCrossing(horizontal: boolean, x: number, y: number): RationalPoint | null {
    const key = `${horizontal ? 'h' : 'v'}:${x}:${y}`;
    const cached = this.crossingCache.get(key);
    if (cached !== undefined) return cached;

    const v0 = this.valueAt(x, y);
    const v1 = horizontal ? this.valueAt(x + 1, y) : this.valueAt(x, y + 1);

    let point: RationalPoint | null = null;
    if (this.isInside(v0) !== this.isInside(v1)) {
      this.interpolationCount++;
      // t = (threshold - v0) / (v1 - v0), exact; t in [0, 1].
      const t = Rational.fraction(this.threshold - v0, v1 - v0);
      if (horizontal) {
        point = {
          x: Rational.fraction(BigInt(x) * t.den + t.num, t.den),
          y: Rational.of(y),
        };
      } else {
        point = {
          x: Rational.of(x),
          y: Rational.fraction(BigInt(y) * t.den + t.num, t.den),
        };
      }
    }
    this.crossingCache.set(key, point);
    return point;
  }

  /**
   * Resolves the saddle cases (5 and 10) by comparing the bilinear center
   * value (a+b+c+d)/4 against the threshold. When the center equals the
   * threshold exactly, a fixed diagonal rule applies: the contour connects
   * along the main diagonal (top-left to bottom-right), i.e. the segments
   * wrap corners b and d.
   */
  private saddleSegments(
    caseIndex: number,
    a: bigint,
    b: bigint,
    c: bigint,
    d: bigint,
  ): ReadonlyArray<EdgePair> {
    const sum = a + b + c + d;
    const target = 4n * this.threshold;
    if (sum === target) return SADDLE_ALONG_AC; // fixed diagonal rule
    const centerInside = sum > target;
    if (caseIndex === 5) {
      // a, c inside; b, d outside.
      return centerInside ? SADDLE_ALONG_AC : SADDLE_ALONG_BD;
    }
    // case 10: b, d inside; a, c outside.
    return centerInside ? SADDLE_ALONG_BD : SADDLE_ALONG_AC;
  }

  /**
   * Generates all contour segments. Zero-length segments (both endpoints on
   * the same iso-valued vertex) are dropped, and coincident duplicate
   * segments (arising from flat edges at the threshold between two inside
   * regions) are emitted only once, so no dangling duplicates are produced.
   */
  *generateSegments(): Iterable<Segment> {
    const seen = new Set<string>();
    for (let y = 0; y < this.height - 1; y++) {
      for (let x = 0; x < this.width - 1; x++) {
        const a = this.valueAt(x, y);
        const b = this.valueAt(x + 1, y);
        const c = this.valueAt(x + 1, y + 1);
        const d = this.valueAt(x, y + 1);

        let caseIndex = 0;
        if (this.isInside(a)) caseIndex |= 1;
        if (this.isInside(b)) caseIndex |= 2;
        if (this.isInside(c)) caseIndex |= 4;
        if (this.isInside(d)) caseIndex |= 8;

        let pairs = CASE_TABLE[caseIndex];
        if (pairs === SADDLE) {
          pairs = this.saddleSegments(caseIndex, a, b, c, d);
        }

        for (const [e0, e1] of pairs) {
          const p0 = this.cellEdgeCrossing(e0, x, y);
          const p1 = this.cellEdgeCrossing(e1, x, y);
          if (p0 === null || p1 === null) continue;
          const k0 = pointKey(p0);
          const k1 = pointKey(p1);
          if (k0 === k1) continue; // degenerate zero-length segment
          const segKey = k0 < k1 ? `${k0}|${k1}` : `${k1}|${k0}`;
          if (seen.has(segKey)) continue; // coincident duplicate
          seen.add(segKey);
          yield { a: p0, b: p1, aKey: k0, bKey: k1 };
        }
      }
    }
  }

  private cellEdgeCrossing(
    edge: number,
    x: number,
    y: number,
  ): RationalPoint | null {
    switch (edge) {
      case TOP:
        return this.edgeCrossing(true, x, y);
      case RIGHT:
        return this.edgeCrossing(false, x + 1, y);
      case BOTTOM:
        return this.edgeCrossing(true, x, y + 1);
      case LEFT:
        return this.edgeCrossing(false, x, y);
      default:
        throw new Error(`Unknown cell edge ${edge}`);
    }
  }
}

/** Collects all contour segments for the given grid and threshold. */
export function extractSegments(
  grid: GridInput,
  threshold: number | bigint,
): Segment[] {
  return [...new ContourExtractor(grid, threshold).generateSegments()];
}
