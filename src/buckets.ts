import type { ColumnBucket, DataPoint } from './types.ts';

/** Pixel column for a timestamp on a grid anchored at `origin`. */
export function bucketColumn(time: number, columnWidth: number, origin: number): number {
  return Math.floor((time - origin) / columnWidth);
}

/**
 * Build per-column min/max buckets for points[start, end) (all assumed valid).
 * Ties on value are broken by the smaller original index, which makes the
 * selection associative and therefore safe for incremental merging.
 */
export function buildBuckets(
  points: readonly DataPoint[],
  start: number,
  end: number,
  columnWidth: number,
  origin: number,
  colMin = -Infinity,
  colMax = Infinity,
): ColumnBucket[] {
  const acc = new Map<number, ColumnBucket>();
  for (let i = start; i < end; i++) {
    const column = bucketColumn(points[i].time, columnWidth, origin);
    if (column < colMin || column > colMax) continue;
    const value = points[i].value as number;
    let bucket = acc.get(column);
    if (!bucket) {
      bucket = {
        column,
        minIndex: i, minValue: value,
        maxIndex: i, maxValue: value,
        firstIndex: i, lastIndex: i,
      };
      acc.set(column, bucket);
      continue;
    }
    if (value < bucket.minValue || (value === bucket.minValue && i < bucket.minIndex)) {
      bucket.minValue = value;
      bucket.minIndex = i;
    }
    if (value > bucket.maxValue || (value === bucket.maxValue && i < bucket.maxIndex)) {
      bucket.maxValue = value;
      bucket.maxIndex = i;
    }
    if (i < bucket.firstIndex) bucket.firstIndex = i;
    if (i > bucket.lastIndex) bucket.lastIndex = i;
  }
  return [...acc.values()].sort((a, b) => a.column - b.column);
}

/**
 * Merge finer-level buckets into buckets of the next (doubled-width) zoom
 * level. Level k+1 column c covers exactly level k columns 2c and 2c+1, so
 * the merged result is identical to computing directly from raw points.
 */
export function mergeBuckets(finer: readonly ColumnBucket[]): ColumnBucket[] {
  const acc = new Map<number, ColumnBucket>();
  for (const b of finer) {
    const column = Math.floor(b.column / 2);
    let merged = acc.get(column);
    if (!merged) {
      acc.set(column, { ...b, column });
      continue;
    }
    if (b.minValue < merged.minValue || (b.minValue === merged.minValue && b.minIndex < merged.minIndex)) {
      merged.minValue = b.minValue;
      merged.minIndex = b.minIndex;
    }
    if (b.maxValue > merged.maxValue || (b.maxValue === merged.maxValue && b.maxIndex < merged.maxIndex)) {
      merged.maxValue = b.maxValue;
      merged.maxIndex = b.maxIndex;
    }
    if (b.firstIndex < merged.firstIndex) merged.firstIndex = b.firstIndex;
    if (b.lastIndex > merged.lastIndex) merged.lastIndex = b.lastIndex;
  }
  return [...acc.values()].sort((a, b) => a.column - b.column);
}
