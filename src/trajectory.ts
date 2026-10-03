import { AABB4 } from './aabb4.ts';
import type { Vec4 } from './aabb4.ts';

/** A sampled point of a moving object: position (x, y, z) at time t (epoch ms). */
export interface TrajectoryPoint extends Vec4 {}

/**
 * One straight segment of a piecewise-linear trajectory.
 * The segment is parameterized as p(s) = p0 + s * (p1 - p0), s in [0, 1].
 */
export interface TrajectorySegment {
  /** Identifier of the trajectory (e.g. drone id) this segment belongs to. */
  readonly trajectoryId: string;
  /** Index of the segment inside its trajectory (0-based). */
  readonly segmentIndex: number;
  readonly p0: TrajectoryPoint;
  readonly p1: TrajectoryPoint;
  /** 4D spacetime bounding box of this segment. */
  readonly aabb: AABB4;
}

/** Build the segment list of a trajectory from its sampled points. */
export function buildSegments(
  trajectoryId: string,
  points: readonly TrajectoryPoint[],
): TrajectorySegment[] {
  const segments: TrajectorySegment[] = [];
  for (let i = 0; i + 1 < points.length; i++) {
    const p0 = points[i];
    const p1 = points[i + 1];
    segments.push({
      trajectoryId,
      segmentIndex: i,
      p0,
      p1,
      aabb: AABB4.fromPoints(p0, p1),
    });
  }
  return segments;
}

/**
 * Result of clipping a segment against a 4D query box: the exact
 * sub-fragment of the segment that lies inside the box.
 */
export interface ClippedFragment {
  readonly segment: TrajectorySegment;
  /** Parameter range [s0, s1] within [0, 1] of the original segment. */
  readonly s0: number;
  readonly s1: number;
  /** Exact entry / exit points of the fragment in 4D. */
  readonly enter: TrajectoryPoint;
  readonly exit: TrajectoryPoint;
}

function lerpPoint(seg: TrajectorySegment, s: number): TrajectoryPoint {
  return {
    x: seg.p0.x + s * (seg.p1.x - seg.p0.x),
    y: seg.p0.y + s * (seg.p1.y - seg.p0.y),
    z: seg.p0.z + s * (seg.p1.z - seg.p0.z),
    t: seg.p0.t + s * (seg.p1.t - seg.p0.t),
  };
}

/**
 * Clip a segment against a 4D AABB using the Liang–Barsky slab method
 * generalized to 4 dimensions. Returns the exact in-box fragment, or null
 * when the segment never enters the box.
 *
 * This is stricter than AABB-intersection: a segment whose bounding box
 * overlaps the query box can still miss the box itself, so use this for
 * precise "does the trajectory actually pass through the zone" answers.
 */
export function clipSegmentToBox(
  seg: TrajectorySegment,
  box: AABB4,
): ClippedFragment | null {
  const mins = [box.minX, box.minY, box.minZ, box.minT];
  const maxs = [box.maxX, box.maxY, box.maxZ, box.maxT];
  const p0 = [seg.p0.x, seg.p0.y, seg.p0.z, seg.p0.t];
  const d = [
    seg.p1.x - seg.p0.x,
    seg.p1.y - seg.p0.y,
    seg.p1.z - seg.p0.z,
    seg.p1.t - seg.p0.t,
  ];

  let s0 = 0;
  let s1 = 1;
  for (let axis = 0; axis < 4; axis++) {
    if (d[axis] === 0) {
      // Segment is parallel to this slab: must already be inside it.
      if (p0[axis] < mins[axis] || p0[axis] > maxs[axis]) return null;
      continue;
    }
    let r0 = (mins[axis] - p0[axis]) / d[axis];
    let r1 = (maxs[axis] - p0[axis]) / d[axis];
    if (r0 > r1) {
      const tmp = r0;
      r0 = r1;
      r1 = tmp;
    }
    if (r0 > s0) s0 = r0;
    if (r1 < s1) s1 = r1;
    if (s0 > s1) return null;
  }

  return {
    segment: seg,
    s0,
    s1,
    enter: lerpPoint(seg, s0),
    exit: lerpPoint(seg, s1),
  };
}
