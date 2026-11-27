import type {
  Attributes,
  HistogramDataPoint,
  Metric,
  NumberDataPoint,
  OutputDataPoint,
  Temporality,
} from '../src/types.js';
import { isHistogram } from '../src/validate.js';

export const ns = (value: number): bigint => BigInt(value);

export function numberPoint(
  timestamp: bigint,
  value: number,
  extra: { startTime?: bigint; attributes?: Attributes } = {},
): NumberDataPoint {
  return {
    timestamp,
    value,
    startTime: extra.startTime,
    attributes: extra.attributes,
  };
}

export function histogramPoint(
  timestamp: bigint,
  explicitBounds: number[],
  bucketCounts: number[],
  extra: {
    startTime?: bigint;
    attributes?: Attributes;
    sum?: number;
    min?: number;
    max?: number;
    count?: number;
  } = {},
): HistogramDataPoint {
  const count = extra.count ?? bucketCounts.reduce((total, value) => total + value, 0);
  return {
    timestamp,
    explicitBounds,
    bucketCounts,
    count,
    sum: extra.sum,
    min: extra.min,
    max: extra.max,
    startTime: extra.startTime,
    attributes: extra.attributes,
  };
}

export function metric(
  name: string,
  instrumentType: Metric['instrumentType'],
  temporality: Temporality,
  dataPoints: Metric['dataPoints'],
): Metric {
  return { name, instrumentType, temporality, dataPoints };
}

export function numberValue(point: OutputDataPoint): number {
  if (isHistogram(point)) {
    throw new Error('expected a number output point');
  }
  return point.value;
}

export function asHistogram(point: OutputDataPoint): HistogramDataPoint & {
  restart: boolean;
  resetReason?: string;
} {
  if (!isHistogram(point)) {
    throw new Error('expected a histogram output point');
  }
  return point;
}
