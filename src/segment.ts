import { AABB4, type Vec4 } from './aabb4.ts';

/**
 * One straight leg of a trajectory: linear motion from p0 to p1.
 * Its 4D AABB is what gets stored in the BVH leaves.
 */
export interface TrajectorySegment {
  trajectoryId: string;
  segmentIndex: number;
  p0: Vec4;
  p1: Vec4;
  bounds: AABB4;
}

export function makeSegment(
  trajectoryId: string,
  segmentIndex: number,
  p0: Vec4,
  p1: Vec4,
): TrajectorySegment {
  return { trajectoryId, segmentIndex, p0, p1, bounds: AABB4.fromPoints(p0, p1) };
}

/**
 * Cut a polyline trajectory (ordered 4D samples) into segments.
 */
export function trajectoryToSegments(
  trajectoryId: string,
  points: Vec4[],
): TrajectorySegment[] {
  const segments: TrajectorySegment[] = [];
  for (let i = 0; i + 1 < points.length; i++) {
    segments.push(makeSegment(trajectoryId, i, points[i], points[i + 1]));
  }
  return segments;
}

/**
 * Exact test: does the segment actually pass through the query box?
 *
 * AABB overlap alone gives false positives (the segment may be inside the
 * spatial cube only outside the queried time window, or vice versa). Here we
 * solve, per axis, for the parameter interval s in [0,1] where the linearly
 * interpolated coordinate lies within the box, then intersect all four
 * intervals. Non-empty intersection => the segment truly enters the box.
 */
export function segmentEntersBox(seg: TrajectorySegment, box: AABB4): boolean {
  let lo = 0;
  let hi = 1;

  const clip = (p0: number, p1: number, min: number, max: number): boolean => {
    const d = p1 - p0;
    if (d === 0) {
      return p0 >= min && p0 <= max; // stationary on this axis
    }
    let a = (min - p0) / d;
    let b = (max - p0) / d;
    if (a > b) { const tmp = a; a = b; b = tmp; }
    if (a > lo) lo = a;
    if (b < hi) hi = b;
    return lo <= hi;
  };

  return (
    clip(seg.p0.x, seg.p1.x, box.minX, box.maxX) &&
    clip(seg.p0.y, seg.p1.y, box.minY, box.maxY) &&
    clip(seg.p0.z, seg.p1.z, box.minZ, box.maxZ) &&
    clip(seg.p0.t, seg.p1.t, box.minT, box.maxT)
  );
}
