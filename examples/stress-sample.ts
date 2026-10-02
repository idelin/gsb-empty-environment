/**
 * Demo: run a stress load against the rate limiter while concurrently
 * sampling the metrics interface, then print the final snapshot.
 *
 * Run: node examples/stress-sample.ts   (Node >= 23.6, or >= 22.6 with
 * --experimental-strip-types)
 *
 * Prefers serving real HTTP (/metrics in Prometheus format). In sandboxes
 * where listening sockets are forbidden it falls back to sampling the same
 * handler in-process, so the acceptance flow runs identically either way.
 */

import { TokenBucketLimiter } from "../src/limiter.ts";
import {
  resolveMetricsResponse,
  startMetricsServer,
} from "../src/exporter.ts";

const limiter = new TokenBucketLimiter({
  capacity: 1_000,
  refillPerSec: 20_000, // sustained 20k rps, bursts up to 1k
  windowMs: 1_000,
  windowCount: 10,
});

// Metrics sampler: real HTTP when possible, in-process handler otherwise.
let sample: (path: string) => Promise<string>;
let closeServer: () => Promise<void> = async () => {};
try {
  const { server, url } = await startMetricsServer(limiter, { port: 0 });
  console.log(`metrics endpoint: ${url}/metrics  (Prometheus text format)\n`);
  sample = async (path) => (await fetch(`${url}${path}`)).text();
  closeServer = () => new Promise((r) => server.close(() => r()));
} catch (err) {
  if ((err as NodeJS.ErrnoException).code !== "EPERM") throw err;
  console.log("listen() blocked by sandbox; sampling the metrics handler in-process\n");
  sample = async (path) => resolveMetricsResponse(limiter, "GET", path).body;
}

// --- Stress driver: ~1M requests/s offered against a 20k rps limiter ---
let stop = false;
const driver = (async () => {
  while (!stop) {
    for (let i = 0; i < 5_000; i++) limiter.tryAcquire();
    // A few blocking callers to populate the wait-duration histogram.
    await Promise.all(Array.from({ length: 16 }, () => limiter.acquire()));
  }
})();

// --- Sampler: scrape /metrics every 500ms and show key series ---
const pick = (text: string, name: string): string => {
  const line = text.split("\n").find((l) => l.startsWith(`${name} `));
  return line ? line.split(" ").pop()! : "-";
};

for (let i = 0; i < 6; i++) {
  await new Promise((r) => setTimeout(r, 500));
  const text = await sample("/metrics");
  const allowed = pick(text, "ratelimit_allowed_total");
  const dropped = pick(text, "ratelimit_dropped_total");
  const throughput = pick(text, "ratelimit_throughput_per_second");
  const level = pick(text, "ratelimit_current_token_level");
  const waits = pick(text, "ratelimit_wait_duration_seconds_count");
  const conserved =
    Number(pick(text, "ratelimit_requests_total")) ===
    Number(allowed) + Number(dropped);
  console.log(
    `t=${(i + 1) * 500}ms  allowed=${allowed}  dropped=${dropped}  ` +
      `throughput=${throughput}/s  tokens=${level}  waits=${waits}  ` +
      `conserved=${conserved}`,
  );
}

stop = true;
await driver;

// --- Final JSON snapshot: per-window breakdown ---
const snap = JSON.parse(await sample("/snapshot"));
console.log("\nfinal snapshot:");
console.log(`  requestsTotal      = ${snap.requestsTotal}`);
console.log(`  allowedTotal       = ${snap.allowedTotal}`);
console.log(`  droppedTotal       = ${snap.droppedTotal}`);
console.log(
  `  conservation       = ${snap.allowedTotal + snap.droppedTotal === snap.requestsTotal}`,
);
console.log(`  currentTokenLevel  = ${snap.currentTokenLevel.toFixed(2)}`);
console.log(
  `  wait histogram     = count=${snap.waitDuration.count} ` +
    `mean=${(snap.waitDuration.sumNanos / snap.waitDuration.count / 1e6).toFixed(3)}ms ` +
    `max=${(snap.waitDuration.maxNanos / 1e6).toFixed(3)}ms`,
);
console.log("  per-window (allowed/dropped):");
for (const w of snap.windows) {
  console.log(
    `    ${new Date(w.startMs).toISOString().slice(17, 23)}  ` +
      `allowed=${String(w.allowed).padStart(7)}  dropped=${String(w.dropped).padStart(7)}` +
      `${w.current ? "  (current)" : ""}`,
  );
}

await closeServer();
