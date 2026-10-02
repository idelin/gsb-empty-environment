import { EngineError, ErrorCodes, type ErrorCode } from "../errors.ts";
import { Counter, Gauge, Histogram } from "./primitives.ts";
import { Timer } from "./timer.ts";
import {
  MetricKind,
  seriesKey,
  type Labels,
  type MetricSnapshot,
} from "./types.ts";

type AnyMetric = Counter | Gauge | Histogram;

/**
 * Central registry. All lookups/mutations are synchronous, so concurrent
 * async callers can never observe a partially-created series.
 */
export class MetricsRegistry {
  private readonly series = new Map<string, AnyMetric>();
  private readonly kinds = new Map<string, MetricKind>();

  private register<T extends AnyMetric>(name: string, labels: Labels, kind: MetricKind, create: () => T): T {
    this.assertName(name);
    const key = seriesKey(name, labels);
    const existing = this.series.get(key);
    if (existing !== undefined) {
      if (this.kinds.get(key) !== kind) {
        throw new EngineError(
          ErrorCodes.METRIC_TYPE_CONFLICT,
          `metric ${name} already registered as ${this.kinds.get(key)}, requested ${kind}`,
          { name, labels },
        );
      }
      return existing as T;
    }
    const metric = create();
    this.series.set(key, metric);
    this.kinds.set(key, kind);
    return metric;
  }

  private assertName(name: string): void {
    if (!/^[a-zA-Z_][a-zA-Z0-9_.]*$/.test(name)) {
      throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `illegal metric name "${name}"`);
    }
  }

  counter(name: string, labels: Labels = {}): Counter {
    return this.register(name, labels, MetricKind.Counter, () => new Counter(name, { ...labels }));
  }

  gauge(name: string, labels: Labels = {}): Gauge {
    return this.register(name, labels, MetricKind.Gauge, () => new Gauge(name, { ...labels }));
  }

  histogram(name: string, labels: Labels = {}, buckets?: readonly number[]): Histogram {
    return this.register(name, labels, MetricKind.Histogram, () => new Histogram(name, { ...labels }, buckets));
  }

  timer(name: string, labels: Labels = {}, buckets?: readonly number[]): Timer {
    return new Timer(this.histogram(name, labels, buckets));
  }

  /** Increments the shared error counter for the given code. */
  recordError(code: ErrorCode): void {
    this.counter("errors.total", { code }).inc();
  }

  /** Runs `fn`; if it throws an EngineError, records its code and rethrows. */
  trackErrors<T>(fn: () => T): T {
    try {
      return fn();
    } catch (err) {
      if (err instanceof EngineError) this.recordError(err.code);
      throw err;
    }
  }

  /** Point-in-time snapshots of every series (deep-copied, no shared refs). */
  snapshots(): MetricSnapshot[] {
    const out: MetricSnapshot[] = [];
    for (const metric of this.series.values()) out.push(metric.snapshot());
    return out;
  }

  get size(): number {
    return this.series.size;
  }

  /** Drops every series. After reset the registry holds no references. */
  reset(): void {
    for (const metric of this.series.values()) metric.reset();
    this.series.clear();
    this.kinds.clear();
  }
}
