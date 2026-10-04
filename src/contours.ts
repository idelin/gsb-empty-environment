import { ContourExtractor } from './marching-squares.ts';
import type { GridInput, Segment } from './marching-squares.ts';
import type { RationalPoint } from './rational.ts';

/** Result of contour extraction: open chains and closed rings. */
export interface Contours {
  /** Open polylines (endpoints lie on the grid boundary or dead ends). */
  readonly chains: ReadonlyArray<ReadonlyArray<RationalPoint>>;
  /** Closed polylines; the first point is not repeated at the end. */
  readonly rings: ReadonlyArray<ReadonlyArray<RationalPoint>>;
}

interface HalfEdge {
  seg: number;
  other: string;
}

/**
 * Joins segments into maximal trails. Every segment is used exactly once.
 * At shared vertices with degree > 2 (degenerate iso-valued vertices),
 * segments are paired deterministically in insertion order, so no dangling
 * or duplicated pieces remain.
 */
export function joinSegments(segments: ReadonlyArray<Segment>): Contours {
  const points = new Map<string, RationalPoint>();
  const adjacency = new Map<string, HalfEdge[]>();
  const used = new Array<boolean>(segments.length).fill(false);

  const addHalfEdge = (from: string, to: string, seg: number) => {
    let list = adjacency.get(from);
    if (list === undefined) {
      list = [];
      adjacency.set(from, list);
    }
    list.push({ seg, other: to });
  };

  segments.forEach((segment, i) => {
    points.set(segment.aKey, segment.a);
    points.set(segment.bKey, segment.b);
    addHalfEdge(segment.aKey, segment.bKey, i);
    addHalfEdge(segment.bKey, segment.aKey, i);
  });

  const takeUnused = (key: string): HalfEdge | null => {
    const list = adjacency.get(key);
    if (list === undefined) return null;
    for (const half of list) {
      if (!used[half.seg]) return half;
    }
    return null;
  };

  const chains: RationalPoint[][] = [];
  const rings: RationalPoint[][] = [];

  for (let i = 0; i < segments.length; i++) {
    if (used[i]) continue;
    used[i] = true;
    const trail: string[] = [segments[i].aKey, segments[i].bKey];

    // Extend the tail.
    for (;;) {
      const half = takeUnused(trail[trail.length - 1]);
      if (half === null) break;
      used[half.seg] = true;
      trail.push(half.other);
    }
    // Extend the head.
    for (;;) {
      const half = takeUnused(trail[0]);
      if (half === null) break;
      used[half.seg] = true;
      trail.unshift(half.other);
    }

    const toPoint = (key: string): RationalPoint => points.get(key)!;
    if (trail[0] === trail[trail.length - 1]) {
      trail.pop(); // closed loop: store without the duplicated endpoint
      rings.push(trail.map(toPoint));
    } else {
      chains.push(trail.map(toPoint));
    }
  }

  return { chains, rings };
}

/**
 * Extracts iso-contours of an integer scalar grid at the given threshold.
 *
 * Semantics:
 * - A vertex counts as inside iff its value is strictly greater than the
 *   threshold; crossings are linearly interpolated and stored as exact
 *   rational coordinates.
 * - Saddle cells are resolved by the bilinear center value; if the center
 *   equals the threshold exactly, a fixed diagonal rule connects along the
 *   top-left to bottom-right diagonal.
 * - Each shared edge crossing is computed once; zero-length and coincident
 *   duplicate segments (iso-valued vertices, flat edges) are removed, so
 *   the output contains no dangling duplicate pieces.
 */
export function extractContours(
  grid: GridInput,
  threshold: number | bigint,
): Contours {
  const extractor = new ContourExtractor(grid, threshold);
  return joinSegments([...extractor.generateSegments()]);
}
