import type { DataPoint, MaybeValue, Segment } from './types.ts';

export function isMissing(value: MaybeValue): boolean {
  return value === null || value === undefined || Number.isNaN(value);
}

/** Split a series into maximal runs of valid points, separated by missing values. */
export function computeSegments(points: readonly DataPoint[]): Segment[] {
  const segments: Segment[] = [];
  let start = -1;
  for (let i = 0; i <= points.length; i++) {
    const valid = i < points.length && !isMissing(points[i].value);
    if (valid && start < 0) start = i;
    if (!valid && start >= 0) {
      segments.push({ id: segments.length, startIndex: start, endIndex: i });
      start = -1;
    }
  }
  return segments;
}
