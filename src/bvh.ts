import { AXES } from "./types.ts";
import type { QueryStats, Segment, SpatioTemporalQuery, Vec3 } from "./types.ts";

const LEAF_SIZE = 4;

interface Bounds4 {
  t0: number;
  t1: number;
  min: Vec3;
  max: Vec3;
}

interface BVHNode {
  bounds: Bounds4;
  left: BVHNode | null;
  right: BVHNode | null;
  segments: Segment[] | null; // non-null on leaves
}

function segmentBounds(seg: Segment): Bounds4 {
  const min: Vec3 = { x: 0, y: 0, z: 0 };
  const max: Vec3 = { x: 0, y: 0, z: 0 };
  for (const axis of AXES) {
    min[axis] = Math.min(seg.from[axis], seg.to[axis]);
    max[axis] = Math.max(seg.from[axis], seg.to[axis]);
  }
  return { t0: seg.t0, t1: seg.t1, min, max };
}

function unionBounds(a: Bounds4, b: Bounds4): Bounds4 {
  const min: Vec3 = { x: 0, y: 0, z: 0 };
  const max: Vec3 = { x: 0, y: 0, z: 0 };
  for (const axis of AXES) {
    min[axis] = Math.min(a.min[axis], b.min[axis]);
    max[axis] = Math.max(a.max[axis], b.max[axis]);
  }
  return {
    t0: Math.min(a.t0, b.t0),
    t1: Math.max(a.t1, b.t1),
    min,
    max,
  };
}

function centroid(seg: Segment, axis: keyof Vec3 | "t"): number {
  if (axis === "t") return (seg.t0 + seg.t1) / 2;
  return (seg.from[axis] + seg.to[axis]) / 2;
}

function buildNode(segments: Segment[]): BVHNode {
  let bounds = segmentBounds(segments[0]);
  for (let i = 1; i < segments.length; i++) {
    bounds = unionBounds(bounds, segmentBounds(segments[i]));
  }
  if (segments.length <= LEAF_SIZE) {
    return { bounds, left: null, right: null, segments };
  }
  // Split along the axis (t, x, y, z) with the widest centroid spread.
  const axes = ["t", "x", "y", "z"] as const;
  let bestAxis: (typeof axes)[number] = "t";
  let bestSpread = -1;
  for (const axis of axes) {
    let lo = Infinity;
    let hi = -Infinity;
    for (const seg of segments) {
      const c = centroid(seg, axis);
      if (c < lo) lo = c;
      if (c > hi) hi = c;
    }
    if (hi - lo > bestSpread) {
      bestSpread = hi - lo;
      bestAxis = axis;
    }
  }
  const sorted = [...segments].sort(
    (a, b) => centroid(a, bestAxis) - centroid(b, bestAxis),
  );
  const mid = Math.floor(sorted.length / 2);
  return {
    bounds,
    left: buildNode(sorted.slice(0, mid)),
    right: buildNode(sorted.slice(mid)),
    segments: null,
  };
}

/**
 * Conservative 4D-overlap prune test. Segment times live in [t0, t1) and
 * the query time range is half-open too, so strict time comparisons are
 * safe; spatial overlap uses closed comparisons because the box is closed.
 * Never prunes a true hit; may keep false candidates (filtered later by
 * the exact rational intersection test).
 */
function overlaps(bounds: Bounds4, query: SpatioTemporalQuery): boolean {
  if (bounds.t1 <= query.time.t0 || bounds.t0 >= query.time.t1) return false;
  for (const axis of AXES) {
    if (
      bounds.max[axis] < query.box.min[axis] ||
      bounds.min[axis] > query.box.max[axis]
    ) {
      return false;
    }
  }
  return true;
}

/** BVH over 4D (time + space) segment bounds; candidate pruning only. */
export class BVH {
  readonly root: BVHNode | null;
  readonly size: number;

  private constructor(root: BVHNode | null, size: number) {
    this.root = root;
    this.size = size;
  }

  static build(segments: Segment[]): BVH {
    if (segments.length === 0) return new BVH(null, 0);
    return new BVH(buildNode(segments), segments.length);
  }

  /** Append segments whose 4D bounds overlap the query to `out`. */
  collectCandidates(
    query: SpatioTemporalQuery,
    stats: QueryStats,
    out: Segment[],
  ): void {
    if (!this.root) return;
    const stack: BVHNode[] = [this.root];
    while (stack.length > 0) {
      const node = stack.pop() as BVHNode;
      stats.bvhNodesVisited++;
      if (!overlaps(node.bounds, query)) continue;
      if (node.segments) {
        stats.bvhLeavesVisited++;
        for (const seg of node.segments) {
          stats.candidateSegments++;
          out.push(seg);
        }
      } else {
        if (node.left) stack.push(node.left);
        if (node.right) stack.push(node.right);
      }
    }
  }
}
