import type { Histogram, Labels, MetricsRegistry } from './registry.ts';

export interface DistributionStats {
  count: number;
  min: number | null;
  max: number | null;
  mean: number | null;
  /** Population standard deviation. */
  stddev: number | null;
  p50: number | null;
  p90: number | null;
  p95: number | null;
  p99: number | null;
  /** Samples per second over the aggregation window (0 when no window). */
  ratePerSecond: number;
}

/** Linear-interpolation percentile over sorted values; null when empty. */
export function percentile(sorted: number[], p: number): number | null {
  const n = sorted.length;
  if (n === 0) return null;
  if (p <= 0) return sorted[0];
  if (p >= 100) return sorted[n - 1];
  const rank = (p / 100) * (n - 1);
  const lo = Math.floor(rank);
  const hi = Math.ceil(rank);
  if (lo === hi) return sorted[lo];
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (rank - lo);
}

export function computeStats(values: number[], windowMs?: number): DistributionStats {
  const sorted = [...values].sort((a, b) => a - b);
  const n = sorted.length;
  if (n === 0) {
    return {
      count: 0,
      min: null,
      max: null,
      mean: null,
      stddev: null,
      p50: null,
      p90: null,
      p95: null,
      p99: null,
      ratePerSecond: 0,
    };
  }
  let sum = 0;
  for (const v of sorted) sum += v;
  const mean = sum / n;
  let sq = 0;
  for (const v of sorted) sq += (v - mean) * (v - mean);
  return {
    count: n,
    min: sorted[0],
    max: sorted[n - 1],
    mean,
    stddev: Math.sqrt(sq / n),
    p50: percentile(sorted, 50),
    p90: percentile(sorted, 90),
    p95: percentile(sorted, 95),
    p99: percentile(sorted, 99),
    ratePerSecond: windowMs && windowMs > 0 ? (n / windowMs) * 1000 : 0,
  };
}

export interface HistogramReport extends DistributionStats {
  name: string;
  labels: Labels;
}

export interface CounterReport {
  name: string;
  labels: Labels;
  value: number;
}

export interface GaugeReport {
  name: string;
  labels: Labels;
  value: number;
}

export interface HitRateReport {
  name: string;
  hits: number;
  misses: number;
  /** hits / (hits + misses); null when there were no lookups. */
  hitRate: number | null;
}

export interface AggregateReport {
  timestamp: number;
  windowMs: number | null;
  counters: CounterReport[];
  gauges: GaugeReport[];
  histograms: HistogramReport[];
  hitRates: HitRateReport[];
}

/**
 * On-demand statistical aggregation. Holds no timers or subscriptions of
 * its own: every report is computed from timestamped samples at call time,
 * so there is nothing to leak.
 */
export class Aggregator {
  private readonly registry: MetricsRegistry;

  constructor(registry: MetricsRegistry) {
    this.registry = registry;
  }

  /** Stats for one histogram, optionally restricted to the last `windowMs`. */
  histogramStats(
    histogram: Histogram,
    windowMs?: number,
    now: number = Date.now(),
  ): DistributionStats {
    let samples = histogram.getSamples();
    if (windowMs !== undefined) {
      const cutoff = now - windowMs;
      samples = samples.filter((s) => s.timestamp >= cutoff);
    }
    return computeStats(
      samples.map((s) => s.value),
      windowMs,
    );
  }

  /** Hit rate derived from a pair of counters; null when total is zero. */
  hitRate(
    name: string,
    hitsName: string,
    missesName: string,
    labels?: Labels,
  ): HitRateReport {
    const hits = this.registry.findCounter(hitsName, labels)?.value() ?? 0;
    const misses = this.registry.findCounter(missesName, labels)?.value() ?? 0;
    const total = hits + misses;
    return {
      name,
      hits,
      misses,
      hitRate: total === 0 ? null : hits / total,
    };
  }

  /** Full aggregation across every registered series. */
  collect(windowMs?: number, now: number = Date.now()): AggregateReport {
    return {
      timestamp: now,
      windowMs: windowMs ?? null,
      counters: this.registry.allCounters().map((c) => ({
        name: c.name,
        labels: { ...c.labels },
        value: c.value(),
      })),
      gauges: this.registry.allGauges().map((g) => ({
        name: g.name,
        labels: { ...g.labels },
        value: g.value(),
      })),
      histograms: this.registry.allHistograms().map((h) => ({
        name: h.name,
        labels: { ...h.labels },
        ...this.histogramStats(h, windowMs, now),
      })),
      hitRates: [
        this.hitRate('buffer_pool', 'buffer_pool_hits_total', 'buffer_pool_misses_total'),
      ],
    };
  }
}
