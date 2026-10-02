import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AdaptiveLoadController,
  AcquireTimeoutError,
  DisposedError,
  type Adjustment,
  type LoadSample,
} from '../src/adaptive.ts';

function makeClock(start = 0) {
  let t = start;
  return {
    now: () => t,
    advance: (ms: number) => {
      t += ms;
    },
    set: (v: number) => {
      t = v;
    },
  };
}

const highPressure = (limit: number): LoadSample => ({
  inflight: limit,
  queueDepth: limit * 10,
  errorRate: 0.9,
  latencyMs: 900,
  eventLoopLagMs: 500,
  memoryUsage: 0.95,
});

const lowPressure: LoadSample = {
  inflight: 0,
  queueDepth: 0,
  errorRate: 0,
  latencyMs: 0,
  eventLoopLagMs: 0,
  memoryUsage: 0,
};

// score = 0.3*0.5 + 0.25*1 + 0.2*0.5 = 0.5 -> inside the (0.35, 0.75) hysteresis band
const midPressure = (limit: number): LoadSample => ({
  inflight: 0,
  queueDepth: limit,
  errorRate: 1,
  latencyMs: 100,
  eventLoopLagMs: 0,
  memoryUsage: 0,
});

function makeController(overrides: Record<string, unknown> = {}, clock = makeClock()) {
  const c = new AdaptiveLoadController({
    minLimit: 1,
    maxLimit: 1000,
    initialLimit: 100,
    now: clock.now,
    random: () => 0.5, // zero effective jitter by default
    ...overrides,
  });
  return { controller: c, clock };
}

test('constructor validates options', () => {
  assert.throws(() => new AdaptiveLoadController({ minLimit: 0, maxLimit: 10 }), RangeError);
  assert.throws(() => new AdaptiveLoadController({ minLimit: 10, maxLimit: 5 }), RangeError);
  assert.throws(
    () => new AdaptiveLoadController({ minLimit: 1, maxLimit: 10, growThreshold: 0.8, shrinkThreshold: 0.7 }),
    RangeError,
  );
});

test('shrink: high pressure multiplies the limit down', () => {
  const { controller } = makeController();
  const adj = controller.record(highPressure(100));
  assert.equal(adj?.applied, true);
  assert.equal(adj?.direction, 'shrink');
  assert.equal(adj?.from, 100);
  assert.equal(adj?.to, 70); // floor(100 * 0.7)
  assert.equal(controller.limit, 70);
});

test('shrink: clamps at minLimit and reports suppression at the floor', () => {
  const { controller, clock } = makeController({ minLimit: 1, initialLimit: 2 });
  const a1 = controller.record(highPressure(2));
  assert.equal(a1?.to, 1);
  clock.advance(1000); // past cooldown
  const a2 = controller.record(highPressure(1));
  assert.equal(a2?.applied, false);
  assert.match(a2!.reason, /already at min limit/);
  assert.equal(controller.limit, 1);
});

test('cooldown: further shrink is suppressed until cooldown elapses', () => {
  const { controller, clock } = makeController({ cooldownMs: 1000 });
  controller.record(highPressure(100)); // shrink at t=0
  clock.advance(500);
  const suppressed = controller.record(highPressure(70));
  assert.equal(suppressed?.applied, false);
  assert.match(suppressed!.reason, /cooldown/);
  assert.equal(controller.limit, 70);
  clock.advance(500); // t=1000, cooldown over
  const applied = controller.record(highPressure(70));
  assert.equal(applied?.applied, true);
  // second consecutive applied shrink escalates: floor(70 * 0.7 * 0.85) = 41
  assert.equal(controller.limit, 41);
});

test('escalation: consecutive shrinks compound the shrink factor', () => {
  const { controller, clock } = makeController({ initialLimit: 1000, cooldownMs: 100 });
  const seen: number[] = [];
  for (let i = 0; i < 3; i++) {
    const adj = controller.record(highPressure(controller.limit));
    assert.equal(adj?.applied, true);
    seen.push(adj!.to);
    clock.advance(100);
  }
  // factors: 0.7, 0.7*0.85=0.595, 0.7*0.85^2=0.50575
  assert.deepEqual(seen, [700, 416, 210]);
});

test('hysteresis: mid-band pressure changes nothing and resets streaks', () => {
  const { controller } = makeController({ growStreak: 2 });
  controller.record(lowPressure); // streak 1/2
  const result = controller.record(midPressure(controller.limit));
  assert.equal(result, null);
  assert.equal(controller.limit, 100);
  const again = controller.record(lowPressure);
  assert.equal(again?.applied, false, 'streak was reset by the hysteresis sample');
  assert.match(again!.reason, /growth streak 1\/2/);
});

test('growth: requires a full streak of low-pressure samples', () => {
  const { controller } = makeController({ growStreak: 3, growthStep: 2, jitterPct: 0 });
  assert.match(controller.record(lowPressure)!.reason, /growth streak 1\/3/);
  assert.match(controller.record(lowPressure)!.reason, /growth streak 2\/3/);
  const adj = controller.record(lowPressure);
  assert.equal(adj?.applied, true);
  assert.equal(adj?.direction, 'grow');
  assert.equal(adj?.to, 102);
});

test('growth: jitter scales the step deterministically from the injected RNG', () => {
  const up = makeController({ growStreak: 1, growthStep: 10, jitterPct: 0.5, random: () => 1 });
  assert.equal(up.controller.record(lowPressure)?.to, 115); // step = round(10 * 1.5)
  const down = makeController({ growStreak: 1, growthStep: 10, jitterPct: 0.5, random: () => 0 });
  assert.equal(down.controller.record(lowPressure)?.to, 105); // step = round(10 * 0.5)
});

test('growth: clamps at maxLimit and reports suppression at the ceiling', () => {
  const { controller } = makeController({ maxLimit: 100, initialLimit: 99, growStreak: 1, growthStep: 5 });
  const adj = controller.record(lowPressure);
  assert.equal(adj?.to, 100);
  const capped = controller.record(lowPressure);
  assert.equal(capped?.applied, false);
  assert.match(capped!.reason, /already at max limit/);
});

test('growth: blocked during post-shrink cooldown (anti-avalanche)', () => {
  const { controller, clock } = makeController({ growStreak: 1, cooldownMs: 1000 });
  controller.record(highPressure(100)); // shrink at t=0
  clock.advance(999);
  const adj = controller.record(lowPressure);
  assert.equal(adj?.applied, false);
  assert.match(adj!.reason, /cooldown after shrink/);
  clock.advance(1);
  assert.equal(controller.record(lowPressure)?.applied, true);
});

test('rate limit: adjustments per window are capped', () => {
  const { controller, clock } = makeController({
    growStreak: 1,
    cooldownMs: 0,
    maxAdjustmentsPerWindow: 2,
    windowMs: 5000,
    growthStep: 1,
    jitterPct: 0,
  });
  assert.equal(controller.record(lowPressure)?.applied, true); // grow -> 101
  assert.equal(controller.record(highPressure(101))?.applied, true); // shrink -> 70
  const third = controller.record(lowPressure);
  assert.equal(third?.applied, false);
  assert.match(third!.reason, /rate limit/);
  clock.advance(5001); // window expires
  assert.equal(controller.record(lowPressure)?.applied, true);
});

test('avalanche scenario: spike collapses limit fast, recovery is gradual', () => {
  const { controller, clock } = makeController({
    minLimit: 1,
    maxLimit: 64,
    initialLimit: 64,
    cooldownMs: 100,
    growStreak: 3,
    growthStep: 2,
    jitterPct: 0,
  });
  // sudden avalanche: 3 consecutive spikes
  const collapsed: number[] = [];
  for (let i = 0; i < 3; i++) {
    controller.record(highPressure(controller.limit));
    collapsed.push(controller.limit);
    clock.advance(100);
  }
  assert.deepEqual(collapsed, [44, 26, 13], 'multiplicative escalation damps the collapse');

  // recovery: feed sustained low pressure, growth must be step-wise, never a jump
  let previous = controller.limit;
  let maxStep = 0;
  for (let i = 0; i < 60; i++) {
    controller.record(lowPressure);
    const step = controller.limit - previous;
    assert.ok(step >= 0, 'never shrinks under low pressure');
    maxStep = Math.max(maxStep, step);
    previous = controller.limit;
    clock.advance(10);
  }
  assert.ok(maxStep <= 2, `growth steps bounded by growthStep, saw ${maxStep}`);
  assert.ok(controller.limit < 64, 'full recovery takes many streaks (no thundering herd)');
  assert.ok(controller.limit > 13, 'but recovery does make progress');
});

test('admission: tryAcquire saturates at the limit and emits reject', () => {
  const { controller } = makeController({ initialLimit: 2, minLimit: 1, maxLimit: 2 });
  const rejects: unknown[] = [];
  controller.on('reject', (p) => rejects.push(p));
  const p1 = controller.tryAcquire();
  const p2 = controller.tryAcquire();
  assert.ok(p1 && p2);
  assert.equal(controller.tryAcquire(), null);
  assert.equal(rejects.length, 1);
  assert.equal(controller.inflight, 2);
  p1!.release();
  const p3 = controller.tryAcquire();
  assert.ok(p3, 'capacity freed by release');
  p2!.release();
  p3!.release();
  assert.equal(controller.inflight, 0);
});

test('concurrency: 200 async workers never exceed the dynamic limit', async () => {
  const { controller, clock } = makeController({ initialLimit: 8, minLimit: 1, maxLimit: 64 });
  // shrink the limit mid-flight to prove the moving limit is always respected
  setTimeout(() => controller.record(highPressure(controller.limit)), 5).unref();
  void clock;
  let current = 0;
  let maxSeen = 0;
  let completed = 0;
  const workers = Array.from({ length: 200 }, async () => {
    const permit = await controller.acquire();
    // Capture the limit at grant time: a later shrink must not retroactively
    // invalidate already-running work (inflight may transiently exceed a
    // shrunk limit, which is the correct avalanche-avoidance behavior).
    const limitAtGrant = controller.limit;
    current++;
    maxSeen = Math.max(maxSeen, current);
    assert.ok(current <= limitAtGrant, `inflight ${current} exceeds grant-time limit ${limitAtGrant}`);
    await new Promise((r) => setTimeout(r, Math.random() * 3));
    current--;
    completed++;
    permit.release();
  });
  await Promise.all(workers);
  assert.equal(completed, 200);
  assert.ok(maxSeen <= 8, `peak concurrency ${maxSeen} within limit`);
  assert.equal(controller.inflight, 0, 'no permit leak');
  assert.equal(controller.waiting, 0, 'no waiter leak');
});

test('acquire: timeout rejects and removes the waiter', async () => {
  const { controller } = makeController({ initialLimit: 1, minLimit: 1, maxLimit: 1 });
  const permit = controller.tryAcquire()!;
  await assert.rejects(controller.acquire({ timeoutMs: 20 }), AcquireTimeoutError);
  assert.equal(controller.waiting, 0, 'timed-out waiter is removed');
  permit.release();
  const next = await controller.acquire({ timeoutMs: 1000 });
  next.release();
});

test('permit: release is idempotent (double release cannot corrupt inflight)', () => {
  const { controller } = makeController({ initialLimit: 1, minLimit: 1, maxLimit: 1 });
  const permit = controller.tryAcquire()!;
  assert.equal(permit.release(), true);
  assert.equal(permit.release(), false);
  assert.equal(permit.released, true);
  assert.equal(controller.inflight, 0);
});

test('dispose: pending waiters reject, listeners and timers are fully released', async () => {
  const { controller } = makeController({ initialLimit: 1, minLimit: 1, maxLimit: 1 });
  controller.tryAcquire(); // saturate
  const pending = controller.acquire();
  const rejection = assert.rejects(pending, DisposedError);
  let disposeEvents = 0;
  controller.on('dispose', () => disposeEvents++);
  controller.on('adjust', () => {});
  assert.equal(controller.listenerCount(), 2);
  controller.dispose();
  await rejection;
  assert.equal(disposeEvents, 1);
  assert.equal(controller.listenerCount(), 0, 'all listeners cleared');
  assert.equal(controller.waiting, 0);
  assert.equal(controller.isDisposed, true);
  assert.throws(() => controller.record(lowPressure), DisposedError);
  assert.throws(() => controller.tryAcquire(), DisposedError);
  await assert.rejects(controller.acquire(), DisposedError);
  controller.dispose(); // idempotent
});

test('no-leak: history buffers are strictly bounded under sustained load', () => {
  const { controller } = makeController({
    historySize: 16,
    growStreak: 1,
    cooldownMs: 0,
    maxAdjustmentsPerWindow: 1000,
    growthStep: 1,
    jitterPct: 0,
  });
  for (let i = 0; i < 10000; i++) {
    controller.record(i % 2 === 0 ? lowPressure : highPressure(controller.limit));
  }
  assert.equal(controller.sampleCount, 16, 'sample ring buffer capped');
  assert.ok(controller.adjustmentHistory.length <= 16, 'adjustment ring buffer capped');
});

test('no-leak: 10k acquire/release cycles leave zero residual state', async () => {
  const { controller } = makeController({ initialLimit: 4, minLimit: 1, maxLimit: 4 });
  for (let i = 0; i < 10000; i++) {
    const permit = await controller.acquire();
    permit.release();
  }
  assert.equal(controller.inflight, 0);
  assert.equal(controller.waiting, 0);
  controller.dispose();
  assert.equal(controller.listenerCount(), 0);
});

test('events: adjust listeners observe every applied adjustment', () => {
  const { controller, clock } = makeController({ cooldownMs: 10 });
  const seen: Adjustment[] = [];
  controller.on('adjust', (a) => seen.push(a as Adjustment));
  controller.record(highPressure(100));
  clock.advance(10);
  controller.record(highPressure(70));
  assert.equal(seen.length, 2);
  assert.deepEqual(seen.map((a) => a.to), [70, 41]);
  controller.off('adjust', () => {});
});

test('auto-sample: interval-driven recording stops cleanly on dispose', async () => {
  const { controller } = makeController({ growStreak: 1 });
  controller.startAutoSample(() => lowPressure, 5);
  assert.equal(controller.isAutoSampling, true);
  // Poll with a generous deadline: timing-sensitive fixed sleeps are flaky
  // when the whole suite runs in parallel processes.
  const deadline = Date.now() + 3000;
  while (controller.sampleCount < 3 && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 10));
  }
  const count = controller.sampleCount;
  assert.ok(count >= 3, `expected several samples, got ${count}`);
  controller.stopAutoSample();
  assert.equal(controller.isAutoSampling, false);
  await new Promise((r) => setTimeout(r, 30));
  assert.equal(controller.sampleCount, count, 'no samples after stop');
  controller.dispose();
  assert.equal(controller.isAutoSampling, false);
});

test('score: normalization stays within [0, 1] for extreme inputs', () => {
  const { controller } = makeController();
  assert.equal(controller.score(lowPressure), 0);
  const extreme = controller.score({
    inflight: 1e9,
    queueDepth: 1e9,
    errorRate: 1,
    latencyMs: 1e9,
    eventLoopLagMs: 1e9,
    memoryUsage: 1,
  });
  assert.ok(extreme <= 1 && extreme > 0.99, `extreme load saturates near 1, got ${extreme}`);
});
