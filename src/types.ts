import type { Rational } from "./rational.ts";

export const AXES = ["x", "y", "z"] as const;
export type Axis = (typeof AXES)[number];

/** Integer 3D point. */
export interface Vec3 {
  x: number;
  y: number;
  z: number;
}

/**
 * A linear 3D trajectory segment. Position at time t in [t0, t1) is
 * from + (to - from) * (t - t0) / (t1 - t0). Time is left-closed,
 * right-open. All coordinates and ticks are safe integers.
 */
export interface Segment {
  objectId: string;
  segmentId: string;
  t0: number;
  t1: number;
  from: Vec3;
  to: Vec3;
}

/** Closed axis-aligned 3D box. */
export interface QueryBox {
  min: Vec3;
  max: Vec3;
}

/** Half-open tick range [t0, t1). */
export interface TimeRange {
  t0: number;
  t1: number;
}

export interface SpatioTemporalQuery {
  time: TimeRange;
  box: QueryBox;
}

/**
 * Exact time interval with rational endpoints. `lo` is always included.
 * `hi` is included unless `hiOpen` is true (clamped by a half-open time
 * boundary). A degenerate single-point hit has lo == hi and hiOpen false.
 */
export interface TimeInterval {
  lo: Rational;
  hi: Rational;
  hiOpen: boolean;
}

export interface SegmentHit {
  segmentId: string;
  interval: TimeInterval;
}

export interface IntervalSource {
  segmentId: string;
  interval: TimeInterval;
}

/** Disjoint union component of an object's hit set, with provenance. */
export interface MergedInterval {
  lo: Rational;
  hi: Rational;
  hiOpen: boolean;
  sources: IntervalSource[];
}

export interface ObjectResult {
  objectId: string;
  /** Trajectory version the result was computed from. */
  version: number;
  intervals: MergedInterval[];
}

export interface QueryStats {
  bvhNodesVisited: number;
  bvhLeavesVisited: number;
  candidateSegments: number;
  exactIntersectionTests: number;
  hitSegments: number;
  totalSegments: number;
}

export interface QueryResult {
  /** Snapshot version the query ran against. */
  version: number;
  objects: ObjectResult[];
  stats: QueryStats;
}
