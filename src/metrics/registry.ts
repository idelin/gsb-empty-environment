import { LatencyHistogram, type HistogramSnapshot } from './histogram.ts';

export interface HitRateSnapshot {
  hits: number;
  misses: number;
  ratio: number;
}

export interface RegistrySnapshot {
  counters: Record<string, number>;
  gauges: Record<string, number>;
  hitRates: Record<string, HitRateSnapshot>;
  histograms: Record<string, HistogramSnapshot>;
}

/**
 * In-process metrics registry. All mutation methods are synchronous and
 * await-free, so concurrent async tasks cannot interleave a
 * read-modify-write and corrupt totals.
 */
export class MetricsRegistry {
  private readonly counters = new Map<string, number>();
  private readonly gauges = new Map<string, number>();
  private readonly hitCounters = new Map<string, { hits: number; misses: number }>();
  private readonly histograms = new Map<string, LatencyHistogram>();

  incCounter(name: string, delta = 1): void {
    this.counters.set(name, (this.counters.get(name) ?? 0) + delta);
  }

  getCounter(name: string): number {
    return this.counters.get(name) ?? 0;
  }

  setGauge(name: string, value: number): void {
    this.gauges.set(name, value);
  }

  incGauge(name: string, delta = 1): void {
    this.gauges.set(name, (this.gauges.get(name) ?? 0) + delta);
  }

  getGauge(name: string): number {
    return this.gauges.get(name) ?? 0;
  }

  recordHit(name: string): void {
    const e = this.hitCounters.get(name) ?? { hits: 0, misses: 0 };
    e.hits++;
    this.hitCounters.set(name, e);
  }

  recordMiss(name: string): void {
    const e = this.hitCounters.get(name) ?? { hits: 0, misses: 0 };
    e.misses++;
    this.hitCounters.set(name, e);
  }

  hitRate(name: string): HitRateSnapshot {
    const e = this.hitCounters.get(name) ?? { hits: 0, misses: 0 };
    const total = e.hits + e.misses;
    return { hits: e.hits, misses: e.misses, ratio: total === 0 ? 0 : e.hits / total };
  }

  histogram(name: string): LatencyHistogram {
    let h = this.histograms.get(name);
    if (!h) {
      h = new LatencyHistogram();
      this.histograms.set(name, h);
    }
    return h;
  }

  /** High-precision latency sample via process.hrtime.bigint(). */
  recordLatency(name: string, startNs: bigint): bigint {
    const elapsed = process.hrtime.bigint() - startNs;
    this.histogram(name).record(Number(elapsed));
    return elapsed;
  }

  /** Returns a stop() closure that records elapsed time into `name`. */
  startTimer(name: string): () => bigint {
    const start = process.hrtime.bigint();
    return () => this.recordLatency(name, start);
  }

  snapshot(): RegistrySnapshot {
    const counters: Record<string, number> = {};
    for (const [k, v] of this.counters) counters[k] = v;
    const gauges: Record<string, number> = {};
    for (const [k, v] of this.gauges) gauges[k] = v;
    const hitRates: Record<string, HitRateSnapshot> = {};
    for (const [k] of this.hitCounters) hitRates[k] = this.hitRate(k);
    const histograms: Record<string, HistogramSnapshot> = {};
    for (const [k, v] of this.histograms) histograms[k] = v.snapshot();
    return { counters, gauges, hitRates, histograms };
  }

  reset(): void {
    this.counters.clear();
    this.gauges.clear();
    this.hitCounters.clear();
    for (const h of this.histograms.values()) h.reset();
  }
}
