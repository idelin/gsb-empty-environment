/** Label set attached to a metric series. */
export type Labels = Record<string, string>;

function labelsKey(labels: Labels | undefined): string {
  if (!labels) return '';
  return Object.keys(labels)
    .sort()
    .map((k) => `${k}=${labels[k]}`)
    .join(',');
}

export class Counter {
  readonly name: string;
  readonly labels: Labels;
  private total = 0;

  constructor(name: string, labels: Labels = {}) {
    this.name = name;
    this.labels = { ...labels };
  }

  inc(amount = 1): void {
    if (!Number.isFinite(amount) || amount < 0) {
      throw new Error('counter increment must be a non-negative finite number');
    }
    this.total += amount;
  }

  value(): number {
    return this.total;
  }
}

export class Gauge {
  readonly name: string;
  readonly labels: Labels;
  private current = 0;

  constructor(name: string, labels: Labels = {}) {
    this.name = name;
    this.labels = { ...labels };
  }

  set(value: number): void {
    if (!Number.isFinite(value)) {
      throw new Error('gauge value must be finite');
    }
    this.current = value;
  }

  inc(amount = 1): void {
    this.set(this.current + amount);
  }

  dec(amount = 1): void {
    this.set(this.current - amount);
  }

  value(): number {
    return this.current;
  }
}

export interface Sample {
  value: number;
  timestamp: number;
}

export interface BucketCount {
  le: number;
  count: number;
}

export class Histogram {
  readonly name: string;
  readonly labels: Labels;
  readonly buckets: number[];
  private readonly bucketCounts: number[];
  private readonly samples: Sample[] = [];
  private totalSum = 0;

  constructor(name: string, labels: Labels = {}, buckets: number[] = []) {
    this.name = name;
    this.labels = { ...labels };
    this.buckets = [...buckets];
    for (let i = 0; i < this.buckets.length; i++) {
      const b = this.buckets[i];
      if (!Number.isFinite(b)) throw new Error('bucket bounds must be finite');
      if (i > 0 && b <= this.buckets[i - 1]) {
        throw new Error('bucket bounds must be strictly increasing');
      }
    }
    this.bucketCounts = new Array(this.buckets.length).fill(0);
  }

  record(value: number, timestamp: number = Date.now()): void {
    if (!Number.isFinite(value)) {
      throw new Error('histogram value must be finite');
    }
    this.samples.push({ value, timestamp });
    this.totalSum += value;
    // First bucket with le >= value (le is inclusive), binary search.
    let lo = 0;
    let hi = this.buckets.length;
    while (lo < hi) {
      const mid = (lo + hi) >>> 1;
      if (this.buckets[mid] >= value) hi = mid;
      else lo = mid + 1;
    }
    for (let i = lo; i < this.buckets.length; i++) {
      this.bucketCounts[i]++;
    }
  }

  count(): number {
    return this.samples.length;
  }

  sum(): number {
    return this.totalSum;
  }

  /** All recorded samples (value + wall-clock timestamp). */
  getSamples(): Sample[] {
    return this.samples.map((s) => ({ ...s }));
  }

  /** Cumulative bucket counts, Prometheus-style (le is inclusive). */
  bucketSnapshot(): BucketCount[] {
    return this.buckets.map((le, i) => ({ le, count: this.bucketCounts[i] }));
  }
}

export interface CounterSnapshot {
  name: string;
  labels: Labels;
  value: number;
}

export interface GaugeSnapshot {
  name: string;
  labels: Labels;
  value: number;
}

export interface HistogramSnapshot {
  name: string;
  labels: Labels;
  count: number;
  sum: number;
  buckets: BucketCount[];
}

export interface RegistrySnapshot {
  counters: CounterSnapshot[];
  gauges: GaugeSnapshot[];
  histograms: HistogramSnapshot[];
}

/**
 * Central registry. All mutation methods are synchronous and therefore
 * atomic with respect to interleaved async callers; iteration during
 * snapshotting copies the maps first, so concurrent recording is safe.
 */
export class MetricsRegistry {
  private counters = new Map<string, Counter>();
  private gauges = new Map<string, Gauge>();
  private histograms = new Map<string, Histogram>();
  private closed = false;

  private ensureOpen(): void {
    if (this.closed) throw new Error('registry is closed');
  }

  counter(name: string, labels?: Labels): Counter {
    this.ensureOpen();
    const key = name + '|' + labelsKey(labels);
    let c = this.counters.get(key);
    if (!c) {
      c = new Counter(name, labels);
      this.counters.set(key, c);
    }
    return c;
  }

  gauge(name: string, labels?: Labels): Gauge {
    this.ensureOpen();
    const key = name + '|' + labelsKey(labels);
    let g = this.gauges.get(key);
    if (!g) {
      g = new Gauge(name, labels);
      this.gauges.set(key, g);
    }
    return g;
  }

  histogram(name: string, buckets: number[] = [], labels?: Labels): Histogram {
    this.ensureOpen();
    const key = name + '|' + labelsKey(labels);
    let h = this.histograms.get(key);
    if (!h) {
      h = new Histogram(name, labels, buckets);
      this.histograms.set(key, h);
    }
    return h;
  }

  findCounter(name: string, labels?: Labels): Counter | undefined {
    return this.counters.get(name + '|' + labelsKey(labels));
  }

  allCounters(): Counter[] {
    return [...this.counters.values()];
  }

  allGauges(): Gauge[] {
    return [...this.gauges.values()];
  }

  allHistograms(): Histogram[] {
    return [...this.histograms.values()];
  }

  /** Nanosecond-resolution timer; call the returned closure to record. */
  startTimer(histogram: Histogram): () => number {
    const start = process.hrtime.bigint();
    return () => {
      const elapsed = Number(process.hrtime.bigint() - start);
      histogram.record(elapsed);
      return elapsed;
    };
  }

  /** Times a synchronous function, recording the duration into `histogram`. */
  time<T>(histogram: Histogram, fn: () => T): T {
    const stop = this.startTimer(histogram);
    try {
      return fn();
    } finally {
      stop();
    }
  }

  snapshot(): RegistrySnapshot {
    return {
      counters: [...this.counters.values()].map((c) => ({
        name: c.name,
        labels: { ...c.labels },
        value: c.value(),
      })),
      gauges: [...this.gauges.values()].map((g) => ({
        name: g.name,
        labels: { ...g.labels },
        value: g.value(),
      })),
      histograms: [...this.histograms.values()].map((h) => ({
        name: h.name,
        labels: { ...h.labels },
        count: h.count(),
        sum: h.sum(),
        buckets: h.bucketSnapshot(),
      })),
    };
  }

  /** Number of distinct metric series held by this registry. */
  size(): number {
    return this.counters.size + this.gauges.size + this.histograms.size;
  }

  /** Releases every series. The registry holds no timers or handles, so
   *  closing fully detaches all retained state. */
  close(): void {
    this.counters.clear();
    this.gauges.clear();
    this.histograms.clear();
    this.closed = true;
  }

  get isClosed(): boolean {
    return this.closed;
  }
}
