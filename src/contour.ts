import { Rational } from "./rational";

/** Integer scalar grid, row-major, `data[y * width + x]`. */
export interface Grid {
  readonly width: number;
  readonly height: number;
  readonly data: ArrayLike<number>;
}

/** Intersection point with exact rational coordinates. */
export interface ContourPoint {
  readonly x: Rational;
  readonly y: Rational;
}

/** Undirected segment between two entries of `points`. */
export interface Segment {
  readonly a: number;
  readonly b: number;
}

/** Assembled polyline. `closed = true` means it is a loop (ring). */
export interface Contour {
  readonly closed: boolean;
  readonly points: ContourPoint[];
}

export interface ExtractStats {
  /** Cells inspected. */
  cellsVisited: number;
  /** Actual edge interpolations performed; shared edges are computed once. */
  edgeInterpolations: number;
  /** Zero-length segments dropped (both ends on the same at-threshold vertex). */
  degenerateSegmentsDropped: number;
  /**
   * Duplicated segments dropped. Contours of regions on both sides of a flat
   * (fully at-threshold) edge coincide geometrically; only one copy is kept.
   */
  duplicateSegmentsDropped: number;
}

export interface ContourResult {
  /** Unique intersection points (deduplicated by exact rational coordinate). */
  readonly points: ContourPoint[];
  /** Cell-local segments referencing `points`. */
  readonly segments: Segment[];
  /** Segments stitched into open chains (`closed: false`) and loops (`closed: true`). */
  readonly contours: Contour[];
  readonly stats: ExtractStats;
}

/** Cell edge identifiers: top, right, bottom, left. */
type EdgeId = 0 | 1 | 2 | 3;
type Pair = readonly [EdgeId, EdgeId];

/**
 * Marching-squares connection table.
 * Case bits: TL=8, TR=4, BR=2, BL=1 (bit set means corner value > threshold).
 * Saddle cases 5 and 10 are resolved separately.
 */
const CASE_PAIRS: Readonly<Record<number, readonly Pair[]>> = {
  1: [[3, 2]],
  2: [[2, 1]],
  3: [[3, 1]],
  4: [[0, 1]],
  6: [[0, 2]],
  7: [[0, 3]],
  8: [[0, 3]],
  9: [[0, 2]],
  11: [[0, 1]],
  12: [[3, 1]],
  13: [[2, 1]],
  14: [[3, 2]],
};

/** Case 5 (TR+BL above). */
const SADDLE5_CENTER_ABOVE: readonly Pair[] = [[0, 3], [1, 2]];
const SADDLE5_CENTER_BELOW: readonly Pair[] = [[0, 1], [3, 2]];
/** Case 10 (TL+BR above). */
const SADDLE10_CENTER_ABOVE: readonly Pair[] = [[0, 1], [3, 2]];
const SADDLE10_CENTER_BELOW: readonly Pair[] = [[0, 3], [1, 2]];
/**
 * Tie-break when the bilinear center equals the threshold: always connect
 * along the TL-BR diagonal, i.e. segments hug the TR and BL corners.
 * Complementary cases 5 and 10 then produce identical geometry.
 */
const SADDLE_TIE: readonly Pair[] = [[0, 1], [3, 2]];

/**
 * Extract iso-contours at `threshold` from an integer scalar grid.
 *
 * Conventions:
 * - A grid vertex counts as "inside" iff its value is strictly greater than
 *   the threshold; a vertex exactly at the threshold therefore yields edge
 *   crossings at parameter t = 0/1, i.e. exactly on that vertex.
 * - Every grid edge is evaluated at most once; the crossing point is shared
 *   by the (up to) two adjacent cells and deduplicated by exact coordinate.
 * - Zero-length segments (both crossings landing on the same at-threshold
 *   vertex) are dropped, so flat edges and iso-valued vertices never emit
 *   dangling or duplicated geometry.
 */
export function extractContours(
  grid: Grid,
  threshold: number | bigint | Rational,
): ContourResult {
  const { width, height, data } = grid;
  if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1) {
    throw new Error(`extractContours: invalid grid size ${width}x${height}`);
  }
  if (data.length !== width * height) {
    throw new Error(
      `extractContours: data length ${data.length} does not match ${width}x${height}`,
    );
  }
  const values = new Array<number>(width * height);
  for (let i = 0; i < values.length; i++) {
    const v = Number(data[i]);
    if (!Number.isInteger(v)) {
      throw new Error(`extractContours: grid values must be integers, got ${data[i]}`);
    }
    values[i] = v;
  }
  const level = threshold instanceof Rational ? threshold : Rational.of(threshold);

  const stats: ExtractStats = {
    cellsVisited: 0,
    edgeInterpolations: 0,
    degenerateSegmentsDropped: 0,
    duplicateSegmentsDropped: 0,
  };
  const points: ContourPoint[] = [];
  const pointIdByKey = new Map<string, number>();
  /** Grid-edge key -> point id, or null when the edge has no crossing. */
  const pointIdByEdge = new Map<string, number | null>();
  const segments: Segment[] = [];
  const seenSegmentKeys = new Set<string>();

  const valueAt = (x: number, y: number): number => values[y * width + x];
  /** Strict "inside" test, exact for rational thresholds (level.den > 0). */
  const isAbove = (v: number): boolean => BigInt(v) * level.den > level.num;

  const addPoint = (p: ContourPoint): number => {
    const key = `${p.x.toString()},${p.y.toString()}`;
    const existing = pointIdByKey.get(key);
    if (existing !== undefined) {
      return existing;
    }
    const id = points.length;
    points.push(p);
    pointIdByKey.set(key, id);
    return id;
  };

  /** Crossing point on the edge (x0,y0)-(x1,y1), computed once per grid edge. */
  const edgeCrossing = (
    key: string,
    x0: number,
    y0: number,
    x1: number,
    y1: number,
  ): number | null => {
    const cached = pointIdByEdge.get(key);
    if (cached !== undefined) {
      return cached;
    }
    const v0 = valueAt(x0, y0);
    const v1 = valueAt(x1, y1);
    let id: number | null = null;
    if (isAbove(v0) !== isAbove(v1)) {
      stats.edgeInterpolations++;
      // t = (level - v0) / (v1 - v0), exact rational in [0, 1].
      const t = level.sub(Rational.of(v0)).div(Rational.of(v1 - v0));
      const x = Rational.of(x0).add(t.mul(Rational.of(x1 - x0)));
      const y = Rational.of(y0).add(t.mul(Rational.of(y1 - y0)));
      id = addPoint({ x, y });
    }
    pointIdByEdge.set(key, id);
    return id;
  };

  for (let cy = 0; cy < height - 1; cy++) {
    for (let cx = 0; cx < width - 1; cx++) {
      stats.cellsVisited++;
      const tl = valueAt(cx, cy);
      const tr = valueAt(cx + 1, cy);
      const br = valueAt(cx + 1, cy + 1);
      const bl = valueAt(cx, cy + 1);
      let caseIndex = 0;
      if (isAbove(tl)) caseIndex |= 8;
      if (isAbove(tr)) caseIndex |= 4;
      if (isAbove(br)) caseIndex |= 2;
      if (isAbove(bl)) caseIndex |= 1;
      if (caseIndex === 0 || caseIndex === 15) {
        continue;
      }

      let pairs: readonly Pair[];
      if (caseIndex === 5 || caseIndex === 10) {
        // Bilinear center value c = (tl + tr + br + bl) / 4, compared exactly:
        // c <=> level  iff  (sum) * level.den <=> 4 * level.num.
        const sum = BigInt(tl) + BigInt(tr) + BigInt(br) + BigInt(bl);
        const lhs = sum * level.den;
        const rhs = 4n * level.num;
        if (lhs === rhs) {
          pairs = SADDLE_TIE;
        } else if (caseIndex === 5) {
          pairs = lhs > rhs ? SADDLE5_CENTER_ABOVE : SADDLE5_CENTER_BELOW;
        } else {
          pairs = lhs > rhs ? SADDLE10_CENTER_ABOVE : SADDLE10_CENTER_BELOW;
        }
      } else {
        pairs = CASE_PAIRS[caseIndex];
      }

      // Edge order must match EdgeId: top, right, bottom, left.
      const edgePoints: (number | null)[] = [
        edgeCrossing(`H:${cx}:${cy}`, cx, cy, cx + 1, cy),
        edgeCrossing(`V:${cx + 1}:${cy}`, cx + 1, cy, cx + 1, cy + 1),
        edgeCrossing(`H:${cx}:${cy + 1}`, cx, cy + 1, cx + 1, cy + 1),
        edgeCrossing(`V:${cx}:${cy}`, cx, cy, cx, cy + 1),
      ];

      for (const [e0, e1] of pairs) {
        const a = edgePoints[e0];
        const b = edgePoints[e1];
        if (a === null || b === null) {
          throw new Error("extractContours: internal case/edge mismatch");
        }
        if (a === b) {
          // Both crossings collapsed onto the same at-threshold vertex.
          stats.degenerateSegmentsDropped++;
          continue;
        }
        const segKey = a < b ? `${a}:${b}` : `${b}:${a}`;
        if (seenSegmentKeys.has(segKey)) {
          // Same geometric segment already emitted by the neighbouring cell
          // across a flat edge; keep exactly one copy.
          stats.duplicateSegmentsDropped++;
          continue;
        }
        seenSegmentKeys.add(segKey);
        segments.push({ a, b });
      }
    }
  }

  const contours = stitchContours(points, segments);
  return { points, segments, contours, stats };
}

/** Greedily join segments into open chains and closed loops. */
function stitchContours(points: ContourPoint[], segments: Segment[]): Contour[] {
  const adjacency: number[][] = points.map(() => []);
  segments.forEach((seg, i) => {
    adjacency[seg.a].push(i);
    adjacency[seg.b].push(i);
  });
  const used = new Array<boolean>(segments.length).fill(false);
  const contours: Contour[] = [];

  const walk = (start: number): number[] => {
    const chain = [start];
    let current = start;
    for (;;) {
      const next = adjacency[current].find((id) => !used[id]);
      if (next === undefined) {
        break;
      }
      used[next] = true;
      const seg = segments[next];
      current = seg.a === current ? seg.b : seg.a;
      chain.push(current);
      if (current === start) {
        break;
      }
    }
    return chain;
  };

  const emit = (chain: number[]): void => {
    const closed = chain.length > 1 && chain[0] === chain[chain.length - 1];
    const ids = closed ? chain.slice(0, -1) : chain;
    contours.push({ closed, points: ids.map((id) => points[id]) });
  };

  // Open chains first: they start at odd-degree vertices (grid boundary).
  for (let pid = 0; pid < points.length; pid++) {
    if (adjacency[pid].length % 2 === 1) {
      while (adjacency[pid].some((id) => !used[id])) {
        emit(walk(pid));
      }
    }
  }
  // Whatever remains forms closed loops.
  for (let i = 0; i < segments.length; i++) {
    if (!used[i]) {
      emit(walk(segments[i].a));
    }
  }
  return contours;
}
