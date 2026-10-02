/**
 * Stress demo: hammer a token-bucket limiter while concurrently sampling
 * the /metrics endpoint, printing live Prometheus snapshots.
 *
 * Mixes non-blocking tryAcquire (admit/drop) with blocking acquire calls
 * (which populate the wait-duration histogram).
 *
 * Run: npm run demo
 */
import { TokenBucketRateLimiter } from "../src/rate-limiter.ts";
import { MetricsHandler } from "../src/exporter.ts";

const limiter = new TokenBucketRateLimiter({
  capacity: 200,
  refillPerSec: 20_000, // 20k tok/s sustained
});
const handler = new MetricsHandler(limiter.metrics);

const tryAcquireRequests = 500_000;
const blockingRequests = 5_000;
const sampleEveryMs = 250;

console.log(
  `stress: ${tryAcquireRequests} tryAcquire + ${blockingRequests} blocking acquire against 20k tok/s (burst 200)`,
);
console.log(`sampling /metrics every ${sampleEveryMs}ms\n`);

let stopSampling = false;
const sampler = (async () => {
  let n = 0;
  while (!stopSampling) {
    const { body } = handler.handle("/metrics");
    const interesting = body
      .split("\n")
      .filter((l) =>
        /^(ratelimit_(allowed_total|dropped_total|token_level|window_(allowed|dropped|throughput_per_sec))|ratelimit_wait_duration_seconds_(count|sum)) /.test(l),
      );
    console.log(`--- sample #${++n} @ ${new Date().toISOString()} ---`);
    console.log(interesting.join("\n"));
    await new Promise((r) => setTimeout(r, sampleEveryMs));
  }
})();

// Blocking acquire load: paces itself to the refill rate, records wait times.
const blockingLoad = (async () => {
  const concurrency = 50;
  let remaining = blockingRequests;
  const worker = async () => {
    while (remaining-- > 0) await limiter.acquire();
  };
  await Promise.all(Array.from({ length: concurrency }, worker));
})();

let allowed = 0;
let dropped = 0;
const start = performance.now();
for (let i = 0; i < tryAcquireRequests; i++) {
  if (limiter.tryAcquire()) allowed++;
  else dropped++;
  if (i % 10_000 === 0) await new Promise((r) => setImmediate(r));
}
await blockingLoad;
const elapsedSec = (performance.now() - start) / 1000;
stopSampling = true;
await sampler;

const totalRequests = tryAcquireRequests + blockingRequests;
console.log(`\n--- final tally (${elapsedSec.toFixed(2)}s) ---`);
console.log(`client-side tryAcquire: allowed=${allowed} dropped=${dropped}`);
const snap = limiter.metrics.snapshot();
console.log(`server-side totals:     allowed=${snap.allowedTotal} dropped=${snap.droppedTotal} total=${snap.allowedTotal + snap.droppedTotal}`);
console.log(`wait samples: count=${snap.waitDuration.count} sum=${snap.waitDuration.sum.toFixed(3)}s mean=${(snap.waitDuration.sum / snap.waitDuration.count * 1000).toFixed(2)}ms`);
console.log(
  `conservation: ${snap.allowedTotal + snap.droppedTotal === totalRequests ? "OK" : "VIOLATED"} (allowed+dropped === ${totalRequests})`,
);
console.log(`effective throughput: ${Math.round(snap.allowedTotal / elapsedSec)} req/s admitted`);

console.log(`\n--- final /metrics exposition ---`);
console.log(handler.handle("/metrics").body);
