import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Simulator, StepLimitExceeded, SplitMix64, MinHeap, runWorkload, TraceRecorder } from '../src/index.ts';

test('same seed produces identical event sequences', () => {
  const collect = (seed: number) => {
    const sim = new Simulator(seed);
    const log: string[] = [];
    for (let i = 0; i < 100; i++) {
      sim.schedule(sim.rng.nextInt(50), `e${i}`, () => log.push(`e${i}@${sim.now}`));
    }
    sim.run();
    sim.dispose();
    return log.join(',');
  };
  assert.equal(collect(7), collect(7));
  assert.notEqual(collect(7), collect(8));
});

test('events at same time are ordered by priority then insertion seq', () => {
  const sim = new Simulator(1);
  const order: string[] = [];
  sim.schedule(5, 'c', () => order.push('c'));
  sim.schedule(5, 'a', () => order.push('a'), -1);
  sim.schedule(5, 'b', () => order.push('b'));
  sim.schedule(0, 'first', () => order.push('first'));
  sim.run();
  assert.deepEqual(order, ['first', 'a', 'c', 'b']);
  sim.dispose();
});

test('runUntil stops at time boundary and reports drained=false', () => {
  const sim = new Simulator(1);
  sim.schedule(10, 'late', () => {});
  sim.schedule(2, 'early', () => {});
  const result = sim.run({ untilTime: 5 });
  assert.equal(result.steps, 1);
  assert.equal(result.endTime, 2);
  assert.equal(result.drained, false);
  assert.equal(sim.pending, 1);
  sim.dispose();
});

test('maxSteps guard raises StepLimitExceeded and preserves event', () => {
  const sim = new Simulator(1);
  let fired = 0;
  sim.schedule(0, 'loop', function loop(s) {
    fired++;
    s.schedule(1, 'loop', loop);
  });
  assert.throws(() => sim.run({ maxSteps: 1000 }), (e: unknown) => {
    assert.ok(e instanceof StepLimitExceeded);
    return true;
  });
  assert.equal(fired, 1000);
  assert.equal(sim.pending, 1);
  sim.dispose();
});

test('cancelled events never fire', () => {
  const sim = new Simulator(1);
  let fired = 0;
  const h = sim.schedule(1, 'x', () => fired++);
  h.cancel();
  sim.run();
  assert.equal(fired, 0);
  assert.equal(h.cancelled, true);
  sim.dispose();
});

test('negative or non-finite delays are rejected', () => {
  const sim = new Simulator(1);
  assert.throws(() => sim.schedule(-1, 'a', () => {}), RangeError);
  assert.throws(() => sim.schedule(Number.NaN, 'a', () => {}), RangeError);
  sim.dispose();
});

test('dispose clears all pending state; scheduling after dispose fails', () => {
  const sim = new Simulator(1);
  sim.schedule(1, 'a', () => {});
  sim.schedule(2, 'b', () => {});
  sim.dispose();
  assert.equal(sim.pending, 0);
  assert.equal(sim.isDisposed, true);
  assert.throws(() => sim.schedule(1, 'c', () => {}), /disposed/);
  assert.throws(() => sim.run(), /disposed/);
});

test('SplitMix64 deterministic and bounded', () => {
  const a = new SplitMix64(42);
  const b = new SplitMix64(42);
  for (let i = 0; i < 1000; i++) {
    assert.equal(a.next(), b.next());
  }
  const r = new SplitMix64(1);
  for (let i = 0; i < 1000; i++) {
    const v = r.nextInt(7);
    assert.ok(v >= 0 && v < 7);
  }
  assert.throws(() => r.nextInt(0), RangeError);
});

test('MinHeap pops in (time, priority, seq) order', () => {
  const heap = new MinHeap<{ time: number; priority: number; seq: number; id: number }>();
  const n = 10000;
  const rng = new SplitMix64(99);
  for (let i = 0; i < n; i++) {
    heap.push({ time: rng.nextInt(1000), priority: rng.nextInt(4), seq: i, id: i });
  }
  let prev = { time: -1, priority: -1, seq: -1 };
  let count = 0;
  for (;;) {
    const e = heap.pop();
    if (!e) break;
    const ok =
      e.time > prev.time ||
      (e.time === prev.time && e.priority > prev.priority) ||
      (e.time === prev.time && e.priority === prev.priority && e.seq > prev.seq);
    assert.ok(ok, `heap order violated at ${JSON.stringify(e)} after ${JSON.stringify(prev)}`);
    prev = e;
    count++;
  }
  assert.equal(count, n);
  assert.equal(heap.size, 0);
});

const GOLDEN_MILLION = '31a44fad94dea32b782114cc24b87b86e0eeb24722e6bbfadccf87cffae7ab01';

test('million-step deterministic regression matches golden checksum', { timeout: 120000 }, () => {
  const result = runWorkload({ events: 1_000_000, seed: 42, storeTrace: false });
  assert.equal(result.steps, 2_000_001);
  assert.equal(result.served, 1_000_000);
  assert.equal(result.dropped, 0);
  assert.equal(result.checksum, GOLDEN_MILLION);
});

test('regression is reproducible across independent runs', { timeout: 120000 }, () => {
  const a = runWorkload({ events: 100_000, seed: 1234, storeTrace: false });
  const b = runWorkload({ events: 100_000, seed: 1234, storeTrace: false });
  assert.equal(a.checksum, b.checksum);
  const c = runWorkload({ events: 100_000, seed: 4321, storeTrace: false });
  assert.notEqual(a.checksum, c.checksum);
});

test('capacity overflow drops are counted deterministically', () => {
  const a = runWorkload({ events: 50_000, seed: 5, capacity: 2, storeTrace: false });
  const b = runWorkload({ events: 50_000, seed: 5, capacity: 2, storeTrace: false });
  assert.ok(a.dropped > 0);
  assert.equal(a.checksum, b.checksum);
  assert.equal(a.served + a.dropped, 50_000);
});

test('trace observer records every executed event exactly once', () => {
  const sim = new Simulator(3);
  const rec = new TraceRecorder();
  let fired = 0;
  for (let i = 0; i < 500; i++) {
    sim.schedule(sim.rng.nextInt(100), `e${i}`, () => fired++);
  }
  sim.run({ observer: rec.observer() });
  assert.equal(rec.entries.length, fired);
  assert.deepEqual(rec.entries.map((e) => e.step), [...Array(fired).keys()]);
  sim.dispose();
});
