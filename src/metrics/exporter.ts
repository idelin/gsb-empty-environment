import { writeFileSync } from 'node:fs';
import { Aggregator, type AggregateReport } from './aggregator.ts';
import type { Labels, MetricsRegistry } from './registry.ts';

function escapeLabelValue(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n');
}

function formatLabels(labels: Labels): string {
  const keys = Object.keys(labels).sort();
  if (keys.length === 0) return '';
  return (
    '{' +
    keys.map((k) => `${k}="${escapeLabelValue(labels[k])}"`).join(',') +
    '}'
  );
}

/** JSON export: full aggregate report (counters, gauges, distributions). */
export function toJSON(registry: MetricsRegistry, windowMs?: number): string {
  const report: AggregateReport = new Aggregator(registry).collect(windowMs);
  return JSON.stringify(report, null, 2);
}

/** Prometheus text exposition format. */
export function toPrometheus(registry: MetricsRegistry): string {
  const lines: string[] = [];
  const seenTypes = new Set<string>();
  const typeLine = (name: string, type: string): void => {
    if (!seenTypes.has(name)) {
      lines.push(`# TYPE ${name} ${type}`);
      seenTypes.add(name);
    }
  };

  for (const c of registry.allCounters()) {
    typeLine(c.name, 'counter');
    lines.push(`${c.name}${formatLabels(c.labels)} ${c.value()}`);
  }
  for (const g of registry.allGauges()) {
    typeLine(g.name, 'gauge');
    lines.push(`${g.name}${formatLabels(g.labels)} ${g.value()}`);
  }
  for (const h of registry.allHistograms()) {
    typeLine(h.name, 'histogram');
    const labelEntries = Object.entries(h.labels).sort(([a], [b]) =>
      a < b ? -1 : a > b ? 1 : 0,
    );
    for (const { le, count } of h.bucketSnapshot()) {
      const merged = [...labelEntries, ['le', String(le)] as [string, string]]
        .map(([k, v]) => `${k}="${escapeLabelValue(v)}"`)
        .join(',');
      lines.push(`${h.name}_bucket{${merged}} ${count}`);
    }
    const infLabels = [...labelEntries, ['le', '+Inf'] as [string, string]]
      .map(([k, v]) => `${k}="${escapeLabelValue(v)}"`)
      .join(',');
    lines.push(`${h.name}_bucket{${infLabels}} ${h.count()}`);
    lines.push(`${h.name}_sum${formatLabels(h.labels)} ${h.sum()}`);
    lines.push(`${h.name}_count${formatLabels(h.labels)} ${h.count()}`);
  }
  return lines.join('\n') + '\n';
}

export type ExportFormat = 'json' | 'prometheus';

export function exportToFile(
  registry: MetricsRegistry,
  path: string,
  format: ExportFormat,
  windowMs?: number,
): void {
  const content =
    format === 'json' ? toJSON(registry, windowMs) : toPrometheus(registry);
  writeFileSync(path, content, 'utf8');
}
