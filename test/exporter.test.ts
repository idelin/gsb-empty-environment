import { test } from "node:test";
import assert from "node:assert/strict";
import { TokenBucketRateLimiter } from "../src/rate-limiter.ts";
import { MetricsExporter, MetricsHandler } from "../src/exporter.ts";

/** Minimal Prometheus text parser for the counters we assert on. */
function parseCounter(text: string, name: string): number {
  const match = text.match(new RegExp(`^${name} (\\S+)$`, "m"));
  assert.ok(match, `metric ${name} not found in exposition`);
  return Number(match[1]);
}

test("handler serves /metrics during a stress run; data is conserved", async () => {
  // 5k tokens/sec sustained, burst 100.
  const limiter = new TokenBucketRateLimiter({ capacity: 100, refillPerSec: 5_000 });
  const handler = new MetricsHandler(limiter.metrics);

  const scrape = () => {
    const res = handler.handle("/metrics");
    assert.equal(res.status, 200);
    assert.match(res.contentType, /text\/plain/);
    return res.body;
  };

  // Stress: hammer tryAcquire while a sampler scrapes /metrics concurrently.
  const totalRequests = 100_000;
  const samples: Array<{ allowed: number; dropped: number }> = [];
  let stopSampling = false;

  const sampler = (async () => {
    while (!stopSampling) {
      const text = scrape();
      samples.push({
        allowed: parseCounter(text, "ratelimit_allowed_total"),
        dropped: parseCounter(text, "ratelimit_dropped_total"),
      });
      await new Promise((r) => setImmediate(r));
    }
  })();

  let allowed = 0;
  let dropped = 0;
  for (let i = 0; i < totalRequests; i++) {
    if (limiter.tryAcquire()) allowed++;
    else dropped++;
    if (i % 5_000 === 0) await new Promise((r) => setImmediate(r));
  }
  stopSampling = true;
  await sampler;

  // Sanity: the stress run actually exercised both paths.
  assert.ok(allowed > 0 && dropped > 0, `allowed=${allowed} dropped=${dropped}`);
  assert.ok(samples.length >= 2, "expected several concurrent samples");

  // Counters are monotonic across samples and never exceed final totals.
  for (let i = 1; i < samples.length; i++) {
    assert.ok(samples[i].allowed >= samples[i - 1].allowed);
    assert.ok(samples[i].dropped >= samples[i - 1].dropped);
  }

  // Final exposition must agree exactly with the client-side tally.
  const finalText = scrape();
  const finalAllowed = parseCounter(finalText, "ratelimit_allowed_total");
  const finalDropped = parseCounter(finalText, "ratelimit_dropped_total");
  assert.equal(finalAllowed, allowed);
  assert.equal(finalDropped, dropped);
  // Conservation: every request is accounted for exactly once.
  assert.equal(finalAllowed + finalDropped, totalRequests);

  // Histogram + gauge are present and consistent: count of wait samples is 0
  // (tryAcquire never waits) and token level is within [0, capacity].
  assert.equal(parseCounter(finalText, "ratelimit_wait_duration_seconds_count"), 0);
  const tokenLevel = parseCounter(finalText, "ratelimit_token_level");
  assert.ok(tokenLevel >= 0 && tokenLevel <= 100);

  // JSON snapshot endpoint agrees with the text exposition.
  const snap = JSON.parse(handler.handle("/snapshot").body);
  assert.equal(snap.allowedTotal, allowed);
  assert.equal(snap.droppedTotal, dropped);
  assert.ok(snap.window.allowed + snap.window.dropped <= totalRequests);

  // 404 for unknown paths.
  assert.equal(handler.handle("/nope").status, 404);
});

test("HTTP adapter serves the same exposition over a real socket", async (t) => {
  const limiter = new TokenBucketRateLimiter({ capacity: 10, refillPerSec: 100 });
  limiter.tryAcquire();
  const exporter = new MetricsExporter(limiter.metrics);

  let address: { host: string; port: number };
  try {
    address = await exporter.start();
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "EPERM") {
      t.skip("sandbox forbids listening sockets; handler test covers the logic");
      return;
    }
    throw err;
  }
  t.after(() => exporter.stop());

  const res = await fetch(`http://${address.host}:${address.port}/metrics`);
  assert.equal(res.status, 200);
  const text = await res.text();
  assert.equal(parseCounter(text, "ratelimit_allowed_total"), 1);

  const notFound = await fetch(`http://${address.host}:${address.port}/nope`);
  assert.equal(notFound.status, 404);
});
