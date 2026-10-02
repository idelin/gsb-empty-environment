import { EngineError, ErrorCodes } from "../errors.ts";
import {
  MetricKind,
  canonicalLabels,
  type HistogramSnapshot,
  type MetricSnapshot,
} from "./types.ts";

export interface ExportBundle {
  generatedAt: string;
  metrics: MetricSnapshot[];
  /** Derived ratios computed from raw series (e.g. buffer hit rate). */
  derived: Record<string, number>;
}

/** Full JSON export of a registry snapshot plus derived ratios. */
export function exportJSON(snapshots: MetricSnapshot[]): ExportBundle {
  return {
    generatedAt: new Date().toISOString(),
    metrics: snapshots,
    derived: deriveRatios(snapshots),
  };
}

function findCounter(snapshots: MetricSnapshot[], name: string): number {
  let total = 0;
  for (const s of snapshots) {
    if (s.kind === MetricKind.Counter && s.name === name) total += s.value;
  }
  return total;
}

/** Derived ratios: buffer hit rate, redo apply rate, error total. */
export function deriveRatios(snapshots: MetricSnapshot[]): Record<string, number> {
  const derived: Record<string, number> = {};
  const hits = findCounter(snapshots, "buffer.hit");
  const misses = findCounter(snapshots, "buffer.miss");
  const lookups = hits + misses;
  derived["buffer.hit_rate"] = lookups === 0 ? Number.NaN : hits / lookups;

  const applied = findCounter(snapshots, "recovery.redo.applied");
  const skipped = findCounter(snapshots, "recovery.redo.skipped");
  const redoTotal = applied + skipped;
  derived["recovery.redo.apply_rate"] = redoTotal === 0 ? Number.NaN : applied / redoTotal;

  let errors = 0;
  for (const s of snapshots) {
    if (s.kind === MetricKind.Counter && s.name === "errors.total") errors += s.value;
  }
  derived["errors.total"] = errors;
  return derived;
}

function escapeLabel(value: string): string {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n");
}

function renderLabels(labels: Record<string, string>): string {
  const keys = Object.keys(labels).sort();
  if (keys.length === 0) return "";
  return `{${keys.map((k) => `${k}="${escapeLabel(labels[k]!)}"`).join(",")}}`;
}

/** Prometheus text exposition format. */
export function exportPrometheus(snapshots: MetricSnapshot[]): string {
  const lines: string[] = [];
  for (const s of snapshots) {
    const base = s.name.replace(/\./g, "_");
    if (s.kind === MetricKind.Counter) {
      lines.push(`# TYPE ${base} counter`);
      lines.push(`${base}${renderLabels(s.labels)} ${s.value}`);
    } else if (s.kind === MetricKind.Gauge) {
      lines.push(`# TYPE ${base} gauge`);
      lines.push(`${base}${renderLabels(s.labels)} ${s.value}`);
    } else {
      lines.push(`# TYPE ${base} histogram`);
      const labelStr = canonicalLabels(s.labels);
      const withLe = (le: string) => {
        const merged = labelStr.length === 0 ? `le="${le}"` : `${labelStr},le="${le}"`;
        return `{${merged}}`;
      };
      for (let i = 0; i < s.buckets.length; i++) {
        lines.push(`${base}_bucket${withLe(String(s.buckets[i]))} ${s.bucketCounts[i]}`);
      }
      lines.push(`${base}_bucket${withLe("+Inf")} ${s.bucketCounts[s.bucketCounts.length - 1]}`);
      lines.push(`${base}_sum${renderLabels(s.labels)} ${s.sum}`);
      lines.push(`${base}_count${renderLabels(s.labels)} ${s.count}`);
    }
  }
  return lines.join("\n") + "\n";
}

/**
 * Aggregates several snapshot batches (e.g. from multiple registries or time
 * windows) into one. Counters/gauges are summed; histograms merge bucket
 * counts (bounds must match) and percentiles are estimated from merged buckets.
 */
export function aggregateSnapshots(batches: MetricSnapshot[][]): MetricSnapshot[] {
  const merged = new Map<string, MetricSnapshot>();
  for (const batch of batches) {
    for (const s of batch) {
      const key = `${s.name}|${canonicalLabels(s.labels)}`;
      const prev = merged.get(key);
      if (prev === undefined) {
        merged.set(key, structuredClone(s));
        continue;
      }
      if (prev.kind !== s.kind) {
        throw new EngineError(ErrorCodes.METRIC_TYPE_CONFLICT, `cannot aggregate ${s.name}: kind mismatch`);
      }
      if (s.kind === MetricKind.Counter || s.kind === MetricKind.Gauge) {
        (prev as typeof s).value += s.value;
      } else {
        mergeHistograms(prev as HistogramSnapshot, s);
      }
    }
  }
  return [...merged.values()];
}

function mergeHistograms(target: HistogramSnapshot, src: HistogramSnapshot): void {
  if (target.buckets.length !== src.buckets.length || !target.buckets.every((b, i) => b === src.buckets[i])) {
    throw new EngineError(ErrorCodes.METRIC_INVALID_VALUE, `histogram ${target.name}: bucket bounds mismatch`);
  }
  const counts = target.bucketCounts.map((c, i) => c + src.bucketCounts[i]!);
  const merged: HistogramSnapshot = {
    ...target,
    count: target.count + src.count,
    sum: target.sum + src.sum,
    min: target.count === 0 ? src.min : src.count === 0 ? target.min : Math.min(target.min, src.min),
    max: Math.max(target.max, src.max),
    bucketCounts: counts,
    percentiles: estimatePercentiles(target.buckets, counts),
  };
  Object.assign(target, merged);
}

/** Approximate percentiles from cumulative bucket counts (linear interpolation). */
export function estimatePercentiles(
  buckets: readonly number[],
  cumulative: readonly number[],
  ps: readonly number[] = [50, 90, 95, 99],
): Record<string, number> {
  const total = cumulative[cumulative.length - 1] ?? 0;
  const out: Record<string, number> = {};
  for (const p of ps) {
    if (total === 0) {
      out[`p${p}`] = Number.NaN;
      continue;
    }
    const rank = (p / 100) * total;
    let value = buckets[buckets.length - 1]!;
    for (let i = 0; i < cumulative.length; i++) {
      if (cumulative[i]! >= rank) {
        const upper = i < buckets.length ? buckets[i]! : buckets[buckets.length - 1]!;
        const lower = i === 0 ? 0 : (buckets[i - 1] ?? 0);
        const prevCount = i === 0 ? 0 : cumulative[i - 1]!;
        const inBucket = cumulative[i]! - prevCount;
        const frac = inBucket === 0 ? 0 : (rank - prevCount) / inBucket;
        value = lower + frac * (upper - lower);
        break;
      }
    }
    out[`p${p}`] = value;
  }
  return out;
}
