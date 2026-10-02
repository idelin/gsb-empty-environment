/**
 * Fine-grained metrics engine: counters, gauges, latency histograms,
 * hit-rate trackers and error-code tallies, with high-resolution timing
 * and aggregated export (JSON snapshot + Prometheus-style text).
 *
 * Concurrency model: Node runs JS on a single thread, so every mutation is
 * a synchronous, non-awaiting critical section — increments can never be
 * torn by interleaved async tasks. Series are created lazily and bounded by
 * an optional capacity to prevent label-cardinality leaks.
 */

export type Labels = Record<string, string>;

function labelKey(labels?: Labels): string {
  if (!labels) return '';
  const keys = Object.keys(labels).sort();
  return keys.map((k) => `${k}=${labels[k]}`).join(',');
}

export interface SeriesId {
  name: string;
  labels?: Labels;
}

export class Counter {
  private total = 0;
  inc(by = 1): void {
    if (by < 0) throw new RangeError('counter increment must be >= 0');
    this.total += by;
  }
  get value(): number {
    return this.total;
  }
  reset(): void {
    this.total = 0;
  }
}

export class Gauge {
  private current = 0;
  set(v: number): void {
    this.current = v;
  }
  inc(by = 1): void {
    this.current += by;
  }
  dec(by = 1): void {
    this.current -= by;
  }
  get value(): number {
    return this.current;
  }
  reset(): void {
    this.current = 0;
  }
}

export interface HistogramSnapshot {
  count: number;
  sum: number;
  min: number;
  max: number;
  mean: number;
  buckets: { le: number; count: number }[];
  percentiles: Record<string, number>;
}

export class Histogram {
  private readonly bounds: number[];
  private counts: number[];
  private totalCount = 0;
  private totalSum = 0;
  private minV = Number.POSITIVE_INFINITY;
  private maxV = Number.NEGATIVE_INFINITY;

  constructor(bounds: number[]) {
    if (bounds.length === 0) throw new RangeError('histogram needs at least one bucket bound');
    this.bounds = [...bounds].sort((a, b) => a - b);
    this.counts = new Array(this.bounds.length + 1).fill(0);
  }

  observe(v: number): void {
    if (!Number.isFinite(v)) throw new RangeError('histogram value must be finite');
    let i = 0;
    while (i < this.bounds.length && v > this.bounds[i]) i++;
    this.counts[i]++;
    this.totalCount++;
    this.totalSum += v;
    if (v < this.minV) this.minV = v;
    if (v > this.maxV) this.maxV = v;
  }

  get count(): number {
    return this.totalCount;
  }

  reset(): void {
    this.counts.fill(0);
    this.totalCount = 0;
    this.totalSum = 0;
    this.minV = Number.POSITIVE_INFINITY;
    this.maxV = Number.NEGATIVE_INFINITY;
  }

  /** Linear-interpolated percentile within the matched bucket. */
  percentile(p: number): number {
    if (this.totalCount === 0) return 0;
    if (p <= 0) return this.minV;
    if (p >= 100) return this.maxV;
    const rank = (p / 100) * this.totalCount;
    let cum = 0;
    for (let i = 0; i < this.counts.length; i++) {
      const prev = cum;
      cum += this.counts[i];
      if (cum >= rank && this.counts[i] > 0) {
        const lower = i === 0 ? 0 : this.bounds[i - 1];
        const upper = i < this.bounds.length ? this.bounds[i] : this.maxV;
        const frac = this.counts[i] === 0 ? 0 : (rank - prev) / this.counts[i];
        return lower + (upper - lower) * Math.min(1, Math.max(0, frac));
      }
    }
    return this.maxV;
  }

  snapshot(percentiles: number[] = [50, 90, 99]): HistogramSnapshot {
    const buckets: { le: number; count: number }[] = [];
    let cum = 0;
    for (let i = 0; i < this.bounds.length; i++) {
      cum += this.counts[i];
      buckets.push({ le: this.bounds[i], count: cum });
    }
    const pct: Record<string, number> = {};
    for (const p of percentiles) pct[`p${p}`] = this.percentile(p);
    return {
      count: this.totalCount,
      sum: this.totalSum,
      min: this.totalCount === 0 ? 0 : this.minV,
      max: this.totalCount === 0 ? 0 : this.maxV,
      mean: this.totalCount === 0 ? 0 : this.totalSum / this.totalCount,
      buckets,
      percentiles: pct,
    };
  }
}

/** Hit/miss tracker exposing a hit rate in [0, 1]. */
export class HitRate {
  private hits = 0;
  private misses = 0;
  hit(): void {
    this.hits++;
  }
  miss(): void {
    this.misses++;
  }
  get total(): number {
    return this.hits + this.misses;
  }
  get rate(): number {
    return this.total === 0 ? 0 : this.hits / this.total;
  }
  get counts(): { hits: number; misses: number } {
    return { hits: this.hits, misses: this.misses };
  }
  reset(): void {
    this.hits = 0;
    this.misses = 0;
  }
}

/** Tally of occurrences keyed by error code string. */
export class ErrorCodeCounter {
  private readonly counts = new Map<string, number>();
  record(code: string): void {
    this.counts.set(code, (this.counts.get(code) ?? 0) + 1);
  }
  get(code: string): number {
    return this.counts.get(code) ?? 0;
  }
  get total(): number {
    let t = 0;
    for (const v of this.counts.values()) t += v;
    return t;
  }
  entries(): Record<string, number> {
    return Object.fromEntries(this.counts);
  }
  reset(): void {
    this.counts.clear();
  }
}

/** High-resolution stopwatch in milliseconds (performance.now backed). */
export class Timer {
  private readonly t0: number;
  constructor(now: () => number = () => performance.now()) {
    this.t0 = now();
  }
  elapsedMs(now: () => number = () => performance.now()): number {
    return now() - this.t0;
  }
}

export interface RegistryOptions {
  /** Maximum number of series per metric kind; guards against label leaks. */
  maxSeriesPerKind?: number;
  /** Latency histogram bucket bounds in milliseconds. */
  latencyBuckets?: number[];
  /** Percentiles computed on export. */
  percentiles?: number[];
}

const DEFAULT_LATENCY_BUCKETS = [0.1, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000];

export interface RegistrySnapshot {
  counters: Record<string, number>;
  gauges: Record<string, number>;
  histograms: Record<string, HistogramSnapshot>;
  hitRates: Record<string, { hits: number; misses: number; rate: number }>;
  errorCodes: Record<string, number>;
  seriesCount: number;
}

export class MetricRegistry {
  private readonly counters = new Map<string, { id: SeriesId; m: Counter }>();
  private readonly gauges = new Map<string, { id: SeriesId; m: Gauge }>();
  private readonly histograms = new Map<string, { id: SeriesId; m: Histogram }>();
  private readonly hitRates = new Map<string, { id: SeriesId; m: HitRate }>();
  readonly errorCodes = new ErrorCodeCounter();
  private readonly maxSeries: number;
  private readonly latencyBuckets: number[];
  private readonly pct: number[];

  constructor(options: RegistryOptions = {}) {
    this.maxSeries = options.maxSeriesPerKind ?? 10_000;
    this.latencyBuckets = options.latencyBuckets ?? DEFAULT_LATENCY_BUCKETS;
    this.pct = options.percentiles ?? [50, 90, 99];
  }

  private acquire<T>(
    pool: Map<string, { id: SeriesId; m: T }>,
    id: SeriesId,
    make: () => T,
  ): T {
    const lk = labelKey(id.labels);
    const key = lk ? `${id.name}{${lk}}` : id.name;
    let entry = pool.get(key);
    if (!entry) {
      if (pool.size >= this.maxSeries) {
        throw new RangeError(
          `metric series capacity exceeded (${this.maxSeries}); possible label-cardinality leak`,
        );
      }
      entry = { id, m: make() };
      pool.set(key, entry);
    }
    return entry.m;
  }

  counter(name: string, labels?: Labels): Counter {
    return this.acquire(this.counters, { name, labels }, () => new Counter());
  }

  gauge(name: string, labels?: Labels): Gauge {
    return this.acquire(this.gauges, { name, labels }, () => new Gauge());
  }

  histogram(name: string, labels?: Labels, buckets?: number[]): Histogram {
    return this.acquire(this.histograms, { name, labels }, () => new Histogram(buckets ?? this.latencyBuckets));
  }

  hitRate(name: string, labels?: Labels): HitRate {
    return this.acquire(this.hitRates, { name, labels }, () => new HitRate());
  }

  /** Time a synchronous operation and record its latency. */
  timeSync<T>(name: string, fn: () => T, labels?: Labels): T {
    const t = new Timer();
    try {
      return fn();
    } finally {
      this.histogram(name, labels).observe(t.elapsedMs());
    }
  }

  /** Time an async operation and record its latency. */
  async timeAsync<T>(name: string, fn: () => Promise<T>, labels?: Labels): Promise<T> {
    const t = new Timer();
    try {
      return await fn();
    } finally {
      this.histogram(name, labels).observe(t.elapsedMs());
    }
  }

  get seriesCount(): number {
    return this.counters.size + this.gauges.size + this.histograms.size + this.hitRates.size;
  }

  /** Aggregated immutable snapshot of every series. */
  snapshot(): RegistrySnapshot {
    const counters: Record<string, number> = {};
    for (const [k, e] of this.counters) counters[k] = e.m.value;
    const gauges: Record<string, number> = {};
    for (const [k, e] of this.gauges) gauges[k] = e.m.value;
    const histograms: Record<string, HistogramSnapshot> = {};
    for (const [k, e] of this.histograms) histograms[k] = e.m.snapshot(this.pct);
    const hitRates: Record<string, { hits: number; misses: number; rate: number }> = {};
    for (const [k, e] of this.hitRates) {
      hitRates[k] = { ...e.m.counts, rate: e.m.rate };
    }
    return {
      counters,
      gauges,
      histograms,
      hitRates,
      errorCodes: this.errorCodes.entries(),
      seriesCount: this.seriesCount,
    };
  }

  /** Prometheus-style text exposition. */
  exportText(): string {
    const lines: string[] = [];
    for (const [k, e] of this.counters) lines.push(`# TYPE counter`, `${k} ${e.m.value}`);
    for (const [k, e] of this.gauges) lines.push(`# TYPE gauge`, `${k} ${e.m.value}`);
    for (const [k, e] of this.histograms) {
      const s = e.m.snapshot(this.pct);
      lines.push('# TYPE histogram');
      for (const b of s.buckets) lines.push(`${k}_bucket{le="${b.le}"} ${b.count}`);
      lines.push(`${k}_bucket{le="+Inf"} ${s.count}`, `${k}_sum ${s.sum}`, `${k}_count ${s.count}`);
    }
    for (const [k, e] of this.hitRates) lines.push('# TYPE hit_rate', `${k} ${e.m.rate}`);
    for (const [code, n] of Object.entries(this.errorCodes.entries())) {
      lines.push(`error_code_total{code="${code}"} ${n}`);
    }
    return lines.join('\n') + '\n';
  }

  exportJSON(): string {
    return JSON.stringify(this.snapshot());
  }

  /** Drop every series and tally; post-condition: seriesCount === 0. */
  reset(): void {
    this.counters.clear();
    this.gauges.clear();
    this.histograms.clear();
    this.hitRates.clear();
    this.errorCodes.reset();
  }
}
