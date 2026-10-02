import type { MetricsRegistry, RegistrySnapshot } from './registry.ts';

/** JSON-serialisable statistical aggregation of all registered metrics. */
export function exportJson(registry: MetricsRegistry): RegistrySnapshot {
  return registry.snapshot();
}

/** Flat Prometheus-style text exposition for external scrapers. */
export function exportText(registry: MetricsRegistry): string {
  const snap = registry.snapshot();
  const lines: string[] = [];
  for (const [name, value] of Object.entries(snap.counters)) {
    lines.push(`${name} ${value}`);
  }
  for (const [name, value] of Object.entries(snap.gauges)) {
    lines.push(`${name} ${value}`);
  }
  for (const [name, hr] of Object.entries(snap.hitRates)) {
    lines.push(`${name}_hits ${hr.hits}`);
    lines.push(`${name}_misses ${hr.misses}`);
    lines.push(`${name}_ratio ${hr.ratio}`);
  }
  for (const [name, h] of Object.entries(snap.histograms)) {
    lines.push(`${name}_count ${h.count}`);
    lines.push(`${name}_sum_ns ${h.sumNs}`);
    lines.push(`${name}_mean_ns ${h.meanNs}`);
    lines.push(`${name}_p50_ns ${h.percentiles.p50}`);
    lines.push(`${name}_p90_ns ${h.percentiles.p90}`);
    lines.push(`${name}_p99_ns ${h.percentiles.p99}`);
    lines.push(`${name}_max_ns ${h.maxNs}`);
  }
  return lines.join('\n') + '\n';
}
