import { EngineError, ErrorCodes } from "../errors.ts";
import {
  MetricKind,
  type CounterSnapshot,
  type GaugeSnapshot,
  type HistogramSnapshot,
  type Labels,
} from "./types.ts";

function assertValidDelta(value: number, what: string): void {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `${what} must be a finite number, got ${value}`);
  }
}

/** Monotonic counter. All mutations are synchronous, hence atomic in the event loop. */
export class Counter {
  readonly kind = MetricKind.Counter;
  private value = 0;
  readonly name: string;
  readonly labels: Labels;

  constructor(name: string, labels: Labels) {
    this.name = name;
    this.labels = labels;
  }

  inc(delta = 1): void {
    assertValidDelta(delta, "counter delta");
    if (delta < 0) {
      throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `counter delta must be >= 0, got ${delta}`);
    }
    this.value += delta;
  }

  get(): number {
    return this.value;
  }

  reset(): void {
    this.value = 0;
  }

  snapshot(): CounterSnapshot {
    return { kind: this.kind, name: this.name, labels: { ...this.labels }, value: this.value };
  }
}

/** Gauge that can go up and down. */
export class Gauge {
  readonly kind = MetricKind.Gauge;
  private value = 0;
  readonly name: string;
  readonly labels: Labels;

  constructor(name: string, labels: Labels) {
    this.name = name;
    this.labels = labels;
  }

  set(value: number): void {
    assertValidDelta(value, "gauge value");
    this.value = value;
  }

  inc(delta = 1): void {
    assertValidDelta(delta, "gauge delta");
    this.value += delta;
  }

  dec(delta = 1): void {
    assertValidDelta(delta, "gauge delta");
    this.value -= delta;
  }

  get(): number {
    return this.value;
  }

  reset(): void {
    this.value = 0;
  }

  snapshot(): GaugeSnapshot {
    return { kind: this.kind, name: this.name, labels: { ...this.labels }, value: this.value };
  }
}

export const DEFAULT_BUCKETS: readonly number[] = [
  0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000,
];

/**
 * Histogram with cumulative le-buckets plus retained samples for exact
 * percentiles. `reset()` drops the sample array so no memory is retained.
 */
export class Histogram {
  readonly kind = MetricKind.Histogram;
  readonly buckets: readonly number[];
  readonly name: string;
  readonly labels: Labels;
  private readonly bucketCounts: number[];
  private values: number[] = [];
  private sum = 0;
  private min = Number.POSITIVE_INFINITY;
  private max = Number.NEGATIVE_INFINITY;

  constructor(name: string, labels: Labels, buckets: readonly number[] = DEFAULT_BUCKETS) {
    this.name = name;
    this.labels = labels;
    if (buckets.length === 0) {
      throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, "histogram requires at least one bucket");
    }
    const sorted = [...buckets].sort((a, b) => a - b);
    for (let i = 0; i < sorted.length; i++) {
      const b = sorted[i]!;
      if (!Number.isFinite(b) || b < 0) {
        throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `invalid bucket bound ${b}`);
      }
      if (i > 0 && b === sorted[i - 1]) {
        throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `duplicate bucket bound ${b}`);
      }
    }
    this.buckets = sorted;
    this.bucketCounts = new Array<number>(sorted.length + 1).fill(0); // last slot = +Inf
  }

  observe(value: number): void {
    assertValidDelta(value, "histogram observation");
    if (value < 0) {
      throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `histogram observation must be >= 0, got ${value}`);
    }
    this.values.push(value);
    this.sum += value;
    if (value < this.min) this.min = value;
    if (value > this.max) this.max = value;
    let placed = false;
    for (let i = 0; i < this.buckets.length; i++) {
      if (value <= this.buckets[i]!) {
        this.bucketCounts[i]!++;
        placed = true;
        break;
      }
    }
    if (!placed) this.bucketCounts[this.bucketCounts.length - 1]!++;
  }

  get count(): number {
    return this.values.length;
  }

  get totalSum(): number {
    return this.sum;
  }

  /** Exact percentile in [0,100]; NaN when no samples were recorded. */
  percentile(p: number): number {
    if (this.values.length === 0) return Number.NaN;
    if (p < 0 || p > 100 || !Number.isFinite(p)) {
      throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `percentile must be in [0,100], got ${p}`);
    }
    const sorted = [...this.values].sort((a, b) => a - b);
    const rank = Math.ceil((p / 100) * sorted.length);
    return sorted[Math.max(0, Math.min(sorted.length - 1, rank - 1))]!;
  }

  /** Bucket counts are cumulative (le semantics), matching Prometheus. */
  cumulativeBuckets(): readonly number[] {
    const out = new Array<number>(this.bucketCounts.length).fill(0);
    let acc = 0;
    for (let i = 0; i < this.bucketCounts.length; i++) {
      acc += this.bucketCounts[i]!;
      out[i] = acc;
    }
    return out;
  }

  reset(): void {
    this.values = [];
    this.sum = 0;
    this.min = Number.POSITIVE_INFINITY;
    this.max = Number.NEGATIVE_INFINITY;
    this.bucketCounts.fill(0);
  }

  snapshot(percentiles: readonly number[] = [50, 90, 95, 99]): HistogramSnapshot {
    const empty = this.values.length === 0;
    const ps: Record<string, number> = {};
    for (const p of percentiles) ps[`p${p}`] = this.percentile(p);
    return {
      kind: this.kind,
      name: this.name,
      labels: { ...this.labels },
      count: this.values.length,
      sum: this.sum,
      min: empty ? 0 : this.min,
      max: empty ? 0 : this.max,
      buckets: this.buckets,
      bucketCounts: this.cumulativeBuckets(),
      percentiles: ps,
    };
  }
}
