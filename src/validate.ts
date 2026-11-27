import type {
  DataPoint,
  HistogramDataPoint,
  Metric,
  NumberDataPoint,
  Temporality,
} from './types.js';

export function isHistogram(point: DataPoint): point is HistogramDataPoint {
  return (point as HistogramDataPoint).bucketCounts !== undefined;
}

export function asNumberPoint(point: DataPoint): NumberDataPoint {
  if (isHistogram(point)) {
    throw new Error('expected a number data point but received a histogram point');
  }
  return point;
}

export function asHistogramPoint(point: DataPoint): HistogramDataPoint {
  if (!isHistogram(point)) {
    throw new Error('expected a histogram data point but received a number point');
  }
  return point;
}

export function assertFinite(value: number, label: string): void {
  if (!Number.isFinite(value)) {
    throw new Error(`${label} must be a finite number, received ${String(value)}`);
  }
}

export function validateHistogram(point: HistogramDataPoint): void {
  const { bucketCounts, explicitBounds, count, sum, min, max } = point;
  if (!Array.isArray(bucketCounts) || bucketCounts.length === 0) {
    throw new Error('histogram point requires a non-empty bucketCounts array');
  }
  if (!Array.isArray(explicitBounds)) {
    throw new Error('histogram point requires an explicitBounds array');
  }
  if (bucketCounts.length !== explicitBounds.length + 1) {
    throw new Error(
      `bucketCounts length ${bucketCounts.length} must be explicitBounds length ` +
        `${explicitBounds.length} plus one overflow bucket`,
    );
  }
  for (let index = 1; index < explicitBounds.length; index += 1) {
    const current = explicitBounds[index];
    const previous = explicitBounds[index - 1];
    if (current === undefined || previous === undefined || !(current > previous)) {
      throw new Error('explicitBounds must be strictly increasing');
    }
  }
  for (const bucketCount of bucketCounts) {
    if (!Number.isInteger(bucketCount) || bucketCount < 0) {
      throw new Error('histogram bucket counts must be non-negative integers');
    }
  }
  if (!Number.isInteger(count) || count < 0) {
    throw new Error('histogram count must be a non-negative integer');
  }
  const bucketSum = bucketCounts.reduce((total, value) => total + value, 0);
  if (bucketSum !== count) {
    throw new Error(`histogram count ${count} does not match sum of bucket counts ${bucketSum}`);
  }
  if (sum !== undefined) {
    assertFinite(sum, 'histogram sum');
  }
  if (min !== undefined) {
    assertFinite(min, 'histogram min');
  }
  if (max !== undefined) {
    assertFinite(max, 'histogram max');
  }
  if (min !== undefined && max !== undefined && min > max) {
    throw new Error('histogram min must not exceed max');
  }
}

/** Strict, element-by-element equality of two explicit boundary arrays. */
export function boundsEqual(left: readonly number[], right: readonly number[]): boolean {
  if (left.length !== right.length) {
    return false;
  }
  for (let index = 0; index < left.length; index += 1) {
    if (left[index] !== right[index]) {
      return false;
    }
  }
  return true;
}

export function validateMetric(metric: Metric, expectedTemporality: Temporality): void {
  if (typeof metric.name !== 'string' || metric.name.length === 0) {
    throw new Error('metric requires a non-empty name');
  }
  if (!Array.isArray(metric.dataPoints)) {
    throw new Error(`metric ${metric.name} requires a dataPoints array`);
  }
  if (metric.temporality !== expectedTemporality && metric.instrumentType !== 'GAUGE') {
    throw new Error(
      `${expectedTemporality === 'CUMULATIVE' ? 'CumulativeToDelta' : 'DeltaToCumulative'} ` +
        `processor only accepts ${expectedTemporality} input, received ${metric.temporality} ` +
        `for metric ${metric.name}`,
    );
  }
  for (const point of metric.dataPoints) {
    if (typeof point.timestamp !== 'bigint') {
      throw new Error(`metric ${metric.name} point timestamp must be a bigint nanosecond value`);
    }
    if (point.startTime !== undefined && typeof point.startTime !== 'bigint') {
      throw new Error(`metric ${metric.name} point startTime must be a bigint nanosecond value`);
    }
    if (isHistogram(point)) {
      validateHistogram(point);
    } else {
      assertFinite(point.value, `metric ${metric.name} point value`);
    }
  }
}

/** Structural clone so emitted points never alias caller-owned mutable arrays. */
export function clonePoint<T extends DataPoint>(point: T): T {
  if (isHistogram(point)) {
    return {
      ...point,
      attributes: point.attributes ? { ...point.attributes } : undefined,
      bucketCounts: [...point.bucketCounts],
      explicitBounds: [...point.explicitBounds],
    } as T;
  }
  return {
    ...point,
    attributes: point.attributes ? { ...point.attributes } : undefined,
  } as T;
}
