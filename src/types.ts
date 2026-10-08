/**
 * Shared types for the segmented time-series downsampler.
 */

/** A value in the series: a finite number, or null/NaN to mark a missing point. */
export type MaybeValue = number | null;

/** Why a point was selected into the downsampled output. */
export type PointRole =
  | 'first' // first point (by original index) of its pixel column bucket
  | 'last' // last point of its pixel column bucket
  | 'min' // minimum value of its pixel column bucket
  | 'max' // maximum value of its pixel column bucket
  | 'boundary-left' // nearest point left of the viewport, kept for cross-edge lines
  | 'boundary-right'; // nearest point right of the viewport, kept for cross-edge lines

/**
 * One point of downsampled output. Always carries evidence about the original
 * index range it summarizes, so callers can trace it back to the raw data.
 */
export interface SampledPoint {
  /** Original index into the input series. */
  index: number;
  /** Original timestamp. */
  time: number;
  /** Original value (never missing: missing points are never emitted). */
  value: number;
  /**
   * Id of the gap-delimited segment this point belongs to. A polyline must
   * never connect two consecutive output points whose segmentId differs:
   * that is exactly how missing-value gaps stay un-bridged.
   */
  segmentId: number;
  /** All roles this point plays (a lone point in a column is first+last+min+max). */
  roles: PointRole[];
  /**
   * Evidence: inclusive original-index range of the column bucket this point
   * summarizes. For boundary points this is the point itself.
   */
  rangeLo: number;
  rangeHi: number;
}

/** A maximal run of consecutive valid (non-missing) points, inclusive bounds. */
export interface Segment {
  id: number;
  start: number;
  end: number;
}

/** Viewport + pixel width describing one zoom level. */
export interface Viewport {
  t0: number;
  t1: number;
  width: number;
}
