import { SlidingWindowHistogram } from '../src/histogram';
import { ExactWindow } from '../src/exact';
import { mulberry32, lognormal } from '../src/random';

const assert = require('assert').strict;

let passed = 0;
let failed = 0;

function test(name: string, fn: () => void): void {
  try {
    fn();
    passed++;
    console.log('ok ' + (passed + failed) + ' - ' + name);
  } catch (err) {
    failed++;
    console.error('not ok ' + (passed + failed) + ' - ' + name);
    console.error(err);
  }
}

function relErr(estimate: number, exact: number): number {
  return Math.abs(estimate - exact) / exact;
}

const CAPACITY = 1000;
const TOLERANCE = 0.005; // acceptance bound: 0.5%

test('empty window yields NaN quantiles', () => {
  const h = new SlidingWindowHistogram({ capacity: CAPACITY });
  assert.ok(Number.isNaN(h.p90()));
  assert.ok(Number.isNaN(h.p99()));
  assert.equal(h.length, 0);
});

test('window fills, then evicts with reverse compensation', () => {
  const h = new SlidingWindowHistogram({ capacity: CAPACITY });
  const rand = mulberry32(42);
  for (let i = 0; i < 5000; i++) {
    h.record(lognormal(rand, 30, 0.8));
  }
  assert.equal(h.length, CAPACITY);
  const sum = h.snapshotCounts().reduce((a, b) => a + b, 0);
  assert.equal(sum, CAPACITY, 'bucket counts must sum to the window size');
});

test('P90/P99 stay within 0.5% of exact on a long-tail stream', () => {
  const h = new SlidingWindowHistogram({ capacity: CAPACITY });
  const exact = new ExactWindow(CAPACITY);
  const rand = mulberry32(7);
  let maxP90 = 0;
  let maxP99 = 0;
  let checks = 0;
  for (let i = 0; i < 20000; i++) {
    const v = lognormal(rand, 30, 1.0);
    h.record(v);
    exact.record(v);
    if (i >= CAPACITY && i % 100 === 0) {
      maxP90 = Math.max(maxP90, relErr(h.p90(), exact.p90()));
      maxP99 = Math.max(maxP99, relErr(h.p99(), exact.p99()));
      checks++;
    }
  }
  console.log(
    '  # long-tail stream: ' + checks + ' checkpoints, max rel err p90=' +
      (maxP90 * 100).toFixed(4) + '% p99=' + (maxP99 * 100).toFixed(4) + '%'
  );
  assert.ok(maxP90 <= TOLERANCE, 'p90 error ' + maxP90 + ' exceeds 0.5%');
  assert.ok(maxP99 <= TOLERANCE, 'p99 error ' + maxP99 + ' exceeds 0.5%');
});

test('P99 falls back quickly after extreme spikes slide out', () => {
  const h = new SlidingWindowHistogram({ capacity: CAPACITY });
  const exact = new ExactWindow(CAPACITY);
  const rand = mulberry32(99);
  const push = (v: number) => {
    h.record(v);
    exact.record(v);
  };

  // Phase 1: fill the window with normal long-tail traffic.
  for (let i = 0; i < CAPACITY; i++) {
    push(lognormal(rand, 30, 0.8));
  }
  const baseline = h.p99();

  // Phase 2: extreme spike burst (50 samples at ~60s).
  for (let i = 0; i < 50; i++) {
    push(60000 + rand() * 1000);
  }
  const duringSpike = h.p99();
  assert.ok(
    duringSpike > baseline * 10,
    'P99 must react to spikes (during=' + duringSpike + ' baseline=' + baseline + ')'
  );

  // Phase 3: push normal traffic until every spike has slid out.
  for (let i = 0; i < CAPACITY; i++) {
    push(lognormal(rand, 30, 0.8));
  }
  const after = h.p99();
  const exactAfter = exact.p99();
  const err = relErr(after, exactAfter);
  console.log(
    '  # spike test: baseline=' + baseline.toFixed(2) + 'ms during=' +
      duringSpike.toFixed(2) + 'ms after=' + after.toFixed(2) +
      'ms exact=' + exactAfter.toFixed(2) + 'ms relErr=' + (err * 100).toFixed(4) + '%'
  );
  assert.ok(
    after < duringSpike / 10,
    'P99 must fall back once spikes leave the window'
  );
  assert.ok(
    err <= TOLERANCE,
    'post-eviction P99 error vs exact must be <= 0.5%, got ' + err * 100 + '%'
  );
  assert.ok(relErr(after, baseline) < 0.2, 'P99 should return near baseline');
});

test('error stays within the theoretical bucket bound at every checkpoint', () => {
  const h = new SlidingWindowHistogram({ capacity: CAPACITY });
  const exact = new ExactWindow(CAPACITY);
  const rand = mulberry32(2024);
  const bound = h.maxRelativeError; // 2^(1/256) - 1 ~= 0.2713%
  for (let i = 0; i < 8000; i++) {
    // Force most mass into the log region (>= 128ms).
    const v = 150 + lognormal(rand, 200, 1.2);
    h.record(v);
    exact.record(v);
    if (i >= CAPACITY && i % 200 === 0) {
      const e99 = relErr(h.p99(), exact.p99());
      const e90 = relErr(h.p90(), exact.p90());
      assert.ok(e99 <= bound, 'p99 err ' + e99 + ' beyond bound ' + bound);
      assert.ok(e90 <= bound, 'p90 err ' + e90 + ' beyond bound ' + bound);
    }
  }
});

test('fixed-region samples: absolute error bounded by half bucket width', () => {
  const h = new SlidingWindowHistogram({ capacity: CAPACITY });
  const exact = new ExactWindow(CAPACITY);
  const rand = mulberry32(5);
  for (let i = 0; i < 3000; i++) {
    const v = rand() * 100; // uniform in [0, 100ms), fixed region
    h.record(v);
    exact.record(v);
  }
  const absErr = Math.abs(h.p99() - exact.p99());
  assert.ok(
    absErr <= h.fixedWidthMs / 2 + 1e-9,
    'fixed-region abs err ' + absErr + ' exceeds half bucket width'
  );
});

test('small windows and single sample behave correctly', () => {
  const h = new SlidingWindowHistogram({ capacity: 4 });
  h.record(10);
  assert.equal(h.p99(), h.bucketValue(h.bucketIndex(10)));
  h.record(20);
  h.record(30);
  h.record(40);
  h.record(50); // evicts 10
  assert.equal(h.length, 4);
  const exact = new ExactWindow(4);
  [20, 30, 40, 50].forEach((v) => exact.record(v));
  assert.ok(relErr(h.p99(), exact.p99()) <= TOLERANCE);
});

console.log('');
console.log('1..' + (passed + failed));
console.log('# pass ' + passed + ', fail ' + failed);
if (failed > 0) {
  process.exitCode = 1;
}
