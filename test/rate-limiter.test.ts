import { test } from "node:test";
import assert from "node:assert/strict";
import { TokenBucketRateLimiter } from "../src/rate-limiter.ts";
import { RateLimiterMetrics } from "../src/metrics.ts";
import { FakeClock } from "./helpers.ts";

function makeLimiter(capacity: number, refillPerSec: number) {
  const clock = new FakeClock();
  const metrics = new RateLimiterMetrics(clock);
  const limiter = new TokenBucketRateLimiter({
    capacity,
    refillPerSec,
    clock,
    metrics,
  });
  return { clock, metrics, limiter };
}

test("tryAcquire admits up to capacity then drops", () => {
  const { metrics, limiter } = makeLimiter(3, 1);
  assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), true);
  assert.equal(limiter.tryAcquire(), false);
  assert.equal(limiter.tryAcquire(), false);

  assert.equal(metrics.allowedTotal, 3);
  assert.equal(metrics.droppedTotal, 2);
  // Conservation: allowed + dropped == total attempts.
  assert.equal(metrics.allowedTotal + metrics.droppedTotal, 5);
});

test("tokens refill over time and level gauge tracks bucket", () => {
  const { clock, metrics, limiter } = makeLimiter(10, 100); // 100 tok/s
  limiter.tryAcquire();
  limiter.tryAcquire();
  assert.equal(metrics.tokenLevel, 8);

  clock.advance(50); // +5 tokens
  assert.equal(limiter.currentTokens, 10); // 8 + 5 = 13, capped at capacity

  clock.advance(1000);
  assert.equal(limiter.currentTokens, 10);
});

test("acquire waits for a token and records wait duration", async () => {
  const { clock, metrics, limiter } = makeLimiter(1, 1000); // 1 token per ms
  assert.equal(limiter.tryAcquire(), true); // drain the bucket

  // With the fake clock frozen, acquire would wait forever; advance it from
  // a timer to simulate refill while acquire sleeps.
  const pending = limiter.acquire();
  clock.advance(5); // 5ms => 5 tokens refilled on next refill check
  const ok = await pending;
  assert.equal(ok, true);

  assert.equal(metrics.allowedTotal, 2);
  assert.equal(metrics.waitDuration.count, 1);
  assert.ok(metrics.waitDuration.sum >= 0);
});

test("acquire times out and counts as drop", async () => {
  const { clock, metrics, limiter } = makeLimiter(1, 1); // 1 token/sec
  assert.equal(limiter.tryAcquire(), true); // drain

  const pending = limiter.acquire(1, 10); // 10ms timeout, need 1000ms
  clock.advance(20);
  const ok = await pending;
  assert.equal(ok, false);
  assert.equal(metrics.droppedTotal, 1);
  assert.equal(metrics.waitDuration.count, 0); // never admitted => no wait sample
});

test("stress: conservation under mixed load", async () => {
  const { metrics, limiter } = makeLimiter(50, 20_000);
  const total = 50_000;
  let allowed = 0;
  let dropped = 0;
  for (let i = 0; i < total; i++) {
    if (limiter.tryAcquire()) allowed++;
    else dropped++;
  }
  assert.equal(allowed + dropped, total);
  assert.equal(metrics.allowedTotal, allowed);
  assert.equal(metrics.droppedTotal, dropped);
  assert.equal(metrics.allowedTotal + metrics.droppedTotal, total);

  const snap = metrics.snapshot();
  assert.equal(snap.window.allowed + snap.window.dropped, total);
});
