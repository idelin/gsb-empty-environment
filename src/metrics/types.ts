/** Label set identifying a metric series. Keys are sorted for canonical form. */
export type Labels = Record<string, string>;

export const MetricKind = {
  Counter: "counter",
  Gauge: "gauge",
  Histogram: "histogram",
} as const;
export type MetricKind = (typeof MetricKind)[keyof typeof MetricKind];

export interface CounterSnapshot {
  kind: typeof MetricKind.Counter;
  name: string;
  labels: Labels;
  value: number;
}

export interface GaugeSnapshot {
  kind: typeof MetricKind.Gauge;
  name: string;
  labels: Labels;
  value: number;
}

export interface HistogramSnapshot {
  kind: typeof MetricKind.Histogram;
  name: string;
  labels: Labels;
  count: number;
  sum: number;
  min: number;
  max: number;
  /** Upper bounds (le) aligned with `bucketCounts`. */
  buckets: readonly number[];
  bucketCounts: readonly number[];
  /** Exact percentiles computed from retained samples. */
  percentiles: Record<string, number>;
}

export type MetricSnapshot = CounterSnapshot | GaugeSnapshot | HistogramSnapshot;

/** Canonical "k=v,k=v" rendering of a label set (keys sorted). */
export function canonicalLabels(labels: Labels): string {
  const keys = Object.keys(labels).sort();
  return keys.map((k) => `${k}=${labels[k]}`).join(",");
}

/** Series key used inside the registry. */
export function seriesKey(name: string, labels: Labels): string {
  return `${name}|${canonicalLabels(labels)}`;
}
