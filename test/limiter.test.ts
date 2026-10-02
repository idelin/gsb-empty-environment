import { test } from "node:test";
import assert from "node:assert/strict";
import { TokenBucketLimiter } from "../src/limiter.ts";

test("tryAcquire admits up to capacity, then drops", () => {
  const limiter = new TokenBucketLimiter({
    capacity: 5,
    refillPerSec: 1,
    nowMs: () => 0, // frozen clock: no refill
  });
  const results = Array.from({ length: 8 }, () => limiter.tryAcquire());
  assert.deepEqual(results, [true, true, true, true, true, false, false, false]);
  const snap = limiter.snapshot();
  assert.equal(snap.allowedTotal, 5);
  assert.equal(snap.droppedTotal, 3);
  assert.equal(snap.requestsTotal, 8);
  assert.equal(snap.currentTokenLevel, 0);
});

test("bucket refills at the configured rate and caps at capacity", () => {
  let now = 0;
  const limiter = new TokenBucketLimiter({
    capacity: 10,
    refillPerSec: 1000, // 1 token per ms
    initialTokens: 0,
    nowMs: () => now,
  });
  assert.equal(limiter.tryAcquire(), false);
  now = 3;
  assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), false);
  // Idle far beyond capacity: level must cap at 10.
  now = 10_000;
  for (let i = 0; i < 10; i++) assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), false);
});

test("conservation holds under mixed stress: allowed + dropped === requests", () => {
  const limiter = new TokenBucketLimiter({
    capacity: 256,
    refillPerSec: 20_000,
  });
  const N = 200_000;
  let allowed = 0;
  for (let i = 0; i < N; i++) {
    if (limiter.tryAcquire()) allowed++;
  }
  const snap = limiter.snapshot();
  assert.equal(snap.allowedTotal, allowed);
  assert.equal(snap.allowedTotal + snap.droppedTotal, N);
  assert.equal(snap.requestsTotal, N);
  assert.ok(snap.droppedTotal > 0, "stress should exceed capacity and drop");
});

test("acquire() waits for a token and records the wait duration", async () => {
  const limiter = new TokenBucketLimiter({
    capacity: 1,
    refillPerSec: 500, // one token every 2ms
    initialTokens: 0,
  });
  const start = performance.now();
  await limiter.acquire();
  const waitedMs = performance.now() - start;
  assert.ok(waitedMs >= 1, `expected to wait for refill, waited ${waitedMs}ms`);
  const snap = limiter.snapshot();
  assert.equal(snap.allowedTotal, 1);
  assert.equal(snap.droppedTotal, 0);
  assert.equal(snap.waitDuration.count, 1);
  assert.ok(snap.waitDuration.sumNanos > 0);
  assert.ok(snap.waitDuration.maxNanos >= snap.waitDuration.minNanos);
});

test("rejects invalid configuration", () => {
  assert.throws(() => new TokenBucketLimiter({ capacity: 0, refillPerSec: 1 }), RangeError);
  assert.throws(() => new TokenBucketLimiter({ capacity: 1, refillPerSec: 0 }), RangeError);
});
