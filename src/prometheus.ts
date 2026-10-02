/**
 * Prometheus text exposition format renderer (v0.0.4).
 * Pure function over a metrics snapshot + live gauges; no allocation on the
 * record path lives here — rendering happens only when scraped.
 */
import type { MetricsSnapshot, RateLimiterMetrics } from "./metrics.ts";

export interface RenderOptions {
  /** Metric name prefix, e.g. "ratelimit". */
  prefix?: string;
}

function fmtNum(value: number): string {
  if (Number.isInteger(value)) return String(value);
  if (Number.isFinite(value)) return String(value);
  if (value === Infinity) return "+Inf";
  if (value === -Infinity) return "-Inf";
  return "NaN";
}

function fmtLe(bound: number): string {
  return fmtNum(bound);
}

/**
 * Render the standard Prometheus text format for a metrics snapshot.
 * Gauges that must be live (token level, in-flight window counters) are
 * taken from `metrics` at scrape time; cumulative counters and the
 * histogram are consistent within the snapshot.
 */
export function renderPrometheus(
  metrics: RateLimiterMetrics,
  snapshot: MetricsSnapshot,
  options: RenderOptions = {},
): string {
  const p = options.prefix ?? "ratelimit";
  const lines: string[] = [];

  lines.push(
    `# HELP ${p}_allowed_total Requests admitted by the rate limiter.`,
    `# TYPE ${p}_allowed_total counter`,
    `${p}_allowed_total ${fmtNum(snapshot.allowedTotal)}`,
    "",
    `# HELP ${p}_dropped_total Requests rejected by the rate limiter.`,
    `# TYPE ${p}_dropped_total counter`,
    `${p}_dropped_total ${fmtNum(snapshot.droppedTotal)}`,
    "",
    `# HELP ${p}_wait_duration_seconds Time requests waited for a token.`,
    `# TYPE ${p}_wait_duration_seconds histogram`,
  );
  for (const bucket of snapshot.waitDuration.buckets) {
    lines.push(
      `${p}_wait_duration_seconds_bucket{le="${fmtLe(bucket.le)}"} ${fmtNum(bucket.count)}`,
    );
  }
  lines.push(
    `${p}_wait_duration_seconds_bucket{le="+Inf"} ${fmtNum(snapshot.waitDuration.count)}`,
    `${p}_wait_duration_seconds_sum ${fmtNum(snapshot.waitDuration.sum)}`,
    `${p}_wait_duration_seconds_count ${fmtNum(snapshot.waitDuration.count)}`,
    "",
    `# HELP ${p}_token_level Current token bucket fill level.`,
    `# TYPE ${p}_token_level gauge`,
    `${p}_token_level ${fmtNum(snapshot.tokenLevel)}`,
    "",
    `# HELP ${p}_window_allowed Requests admitted in the last closed window.`,
    `# TYPE ${p}_window_allowed gauge`,
    `${p}_window_allowed ${fmtNum(snapshot.window.allowed)}`,
    "",
    `# HELP ${p}_window_dropped Requests rejected in the last closed window.`,
    `# TYPE ${p}_window_dropped gauge`,
    `${p}_window_dropped ${fmtNum(snapshot.window.dropped)}`,
    "",
    `# HELP ${p}_window_throughput_per_sec Admitted requests per second over the last closed window.`,
    `# TYPE ${p}_window_throughput_per_sec gauge`,
    `${p}_window_throughput_per_sec ${fmtNum(snapshot.windowThroughputPerSec)}`,
    "",
  );
  return lines.join("\n");
}
