/**
 * Prometheus self-exporter: renders a MetricsSnapshot in the Prometheus
 * text exposition format (version 0.0.4) and serves it over HTTP.
 *
 * The request handling is split into a pure `resolveMetricsResponse` so the
 * full routing/rendering path is testable without opening a socket.
 */

import { createServer } from "node:http";
import type { Server, ServerResponse } from "node:http";
import type { MetricsSnapshot } from "./metrics.ts";
import type { TokenBucketLimiter } from "./limiter.ts";

const formatLe = (le: number): string => {
  if (!Number.isFinite(le)) return "+Inf";
  // Trim to a compact, Prometheus-safe label value.
  return String(Number(le.toPrecision(10)));
};

const formatValue = (v: number): string => {
  if (Number.isInteger(v)) return String(v);
  return String(Number(v.toPrecision(15)));
};

/**
 * Render the standard metrics snapshot as Prometheus text exposition.
 * Counters are cumulative and monotonic; gauges reflect the current instant;
 * the wait-duration histogram uses cumulative `le` buckets as required.
 */
export function renderPrometheus(snapshot: MetricsSnapshot): string {
  const lines: string[] = [];
  const push = (...xs: string[]) => lines.push(...xs);

  push(
    "# HELP ratelimit_requests_total Total admission requests (allowed + dropped).",
    "# TYPE ratelimit_requests_total counter",
    `ratelimit_requests_total ${snapshot.requestsTotal}`,
    "# HELP ratelimit_allowed_total Requests admitted by the rate limiter.",
    "# TYPE ratelimit_allowed_total counter",
    `ratelimit_allowed_total ${snapshot.allowedTotal}`,
    "# HELP ratelimit_dropped_total Requests rejected by the rate limiter.",
    "# TYPE ratelimit_dropped_total counter",
    `ratelimit_dropped_total ${snapshot.droppedTotal}`,
    "# HELP ratelimit_throughput_per_second Admitted requests per second over the last completed window.",
    "# TYPE ratelimit_throughput_per_second gauge",
    `ratelimit_throughput_per_second ${formatValue(snapshot.throughputPerSec)}`,
    "# HELP ratelimit_dropped_last_window Requests dropped in the last completed window.",
    "# TYPE ratelimit_dropped_last_window gauge",
    `ratelimit_dropped_last_window ${snapshot.droppedLastWindow}`,
    "# HELP ratelimit_current_token_level Current token bucket level.",
    "# TYPE ratelimit_current_token_level gauge",
    `ratelimit_current_token_level ${formatValue(snapshot.currentTokenLevel)}`,
    "# HELP ratelimit_window_allowed Requests admitted in the current window.",
    "# TYPE ratelimit_window_allowed gauge",
  );
  const currentWindow = snapshot.windows[snapshot.windows.length - 1];
  push(`ratelimit_window_allowed ${currentWindow ? currentWindow.allowed : 0}`);
  push(
    "# HELP ratelimit_window_dropped Requests dropped in the current window.",
    "# TYPE ratelimit_window_dropped gauge",
    `ratelimit_window_dropped ${currentWindow ? currentWindow.dropped : 0}`,
    "# HELP ratelimit_wait_duration_seconds Time spent waiting for a token.",
    "# TYPE ratelimit_wait_duration_seconds histogram",
  );
  const hist = snapshot.waitDuration;
  for (const bucket of hist.buckets) {
    push(
      `ratelimit_wait_duration_seconds_bucket{le="${formatLe(bucket.le)}"} ${bucket.count}`,
    );
  }
  push(
    `ratelimit_wait_duration_seconds_bucket{le="+Inf"} ${hist.count}`,
    `ratelimit_wait_duration_seconds_sum ${formatValue(hist.sumNanos / 1e9)}`,
    `ratelimit_wait_duration_seconds_count ${hist.count}`,
    "",
  );
  return lines.join("\n");
}

export interface MetricsResponse {
  status: number;
  contentType: string;
  body: string;
}

export const PROMETHEUS_CONTENT_TYPE =
  "text/plain; version=0.0.4; charset=utf-8";

/**
 * Pure request handler: routes a method+path to a metrics response.
 *   GET /metrics  -> Prometheus text exposition format
 *   GET /snapshot -> JSON snapshot (same data, for programmatic sampling)
 */
export function resolveMetricsResponse(
  limiter: TokenBucketLimiter,
  method: string,
  path: string,
): MetricsResponse {
  if (method === "GET" && path === "/metrics") {
    return {
      status: 200,
      contentType: PROMETHEUS_CONTENT_TYPE,
      body: renderPrometheus(limiter.snapshot()),
    };
  }
  if (method === "GET" && path === "/snapshot") {
    return {
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(limiter.snapshot()),
    };
  }
  return { status: 404, contentType: "text/plain", body: "not found\n" };
}

export interface MetricsServerOptions {
  host?: string;
  port?: number;
}

/** Self-contained metrics HTTP server (no external dependencies). */
export function createMetricsServer(
  limiter: TokenBucketLimiter,
  _options: MetricsServerOptions = {},
): Server {
  return createServer((req, res: ServerResponse) => {
    const url = new URL(req.url ?? "/", "http://localhost");
    const { status, contentType, body } = resolveMetricsResponse(
      limiter,
      req.method ?? "GET",
      url.pathname,
    );
    res.writeHead(status, { "content-type": contentType });
    res.end(body);
  });
}

/** Convenience: start listening and resolve with the bound address. */
export function startMetricsServer(
  limiter: TokenBucketLimiter,
  options: MetricsServerOptions = {},
): Promise<{ server: Server; url: string }> {
  const server = createMetricsServer(limiter, options);
  const host = options.host ?? "127.0.0.1";
  const port = options.port ?? 0;
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(port, host, () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        reject(new Error("failed to bind metrics server"));
        return;
      }
      resolve({ server, url: `http://${host}:${address.port}` });
    });
  });
}
