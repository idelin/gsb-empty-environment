import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { irate } from './index.js';
import { s, staleNaN, ordinaryNaN, closeTo } from './test-helpers.js';

describe('irate', () => {
  test('uses only the last two non-stale samples, no extrapolation', () => {
    // Last pair (t=20000,v=5) -> (t=30000,v=20): 15 over 10s => 1.5/s,
    // regardless of earlier samples.
    const samples = [s(0, 2), s(10000, 8), s(20000, 5), s(30000, 20)];
    assert.ok(closeTo(irate(samples, 30000, 30000) as number, 1.5));
  });

  test('counter reset on the final pair adds the absolute new value', () => {
    // 40 -> 5 is a reset: delta = 5 over 5s => 1/s.
    const samples = [s(0, 100), s(20000, 40), s(25000, 5)];
    assert.ok(closeTo(irate(samples, 25000, 25000) as number, 1));
  });

  test('an earlier reset is irrelevant: only the final two samples are used', () => {
    // Reset happens at t=10000 (50 -> 4), but the final pair 4 -> 14 over
    // 10000ms is a normal increase of 10 => 1/s.
    const samples = [s(0, 50), s(10000, 4), s(20000, 14)];
    assert.ok(closeTo(irate(samples, 20000, 20000) as number, 1));
  });

  test('stale markers are skipped when selecting the final pair', () => {
    // Stale at t=25000 must not count; final valid pair is (t=20000,v=8) ->
    // (t=30000,v=20): 12 over 10s = 1.2/s.
    const samples = [s(0, 2), s(20000, 8), s(25000, staleNaN()), s(30000, 20)];
    assert.ok(closeTo(irate(samples, 30000, 30000) as number, 1.2));
  });

  test('reset immediately after a stale marker still applies the reset rule', () => {
    // Last valid pair: (t=20000,v=40) -> (t=30000,v=5), stale in between is
    // dropped, so 40 -> 5 is a reset: delta 5 over 10s = 0.5/s.
    const samples = [s(0, 100), s(20000, 40), s(25000, staleNaN()), s(30000, 5)];
    assert.ok(closeTo(irate(samples, 30000, 30000) as number, 0.5));
  });

  test('only samples inside the window participate', () => {
    // Window [10000, 30000]; the t=0 sample is excluded, so the final pair is
    // (t=20000,v=5) -> (t=30000,v=8): 3 over 10s = 0.3/s.
    const samples = [s(0, 1000), s(20000, 5), s(30000, 8)];
    assert.ok(closeTo(irate(samples, 30000, 20000) as number, 0.3));
  });

  test('fewer than two non-stale samples yields undefined', () => {
    assert.equal(irate([s(0, 1)], 30000, 30000), undefined);
    assert.equal(irate([s(0, 1), s(10000, staleNaN())], 30000, 30000), undefined);
  });

  test('ordinary NaN in the final pair propagates', () => {
    const samples = [s(0, 5), s(15000, ordinaryNaN()), s(30000, 6)];
    const result = irate(samples, 30000, 30000);
    assert.notEqual(result, undefined);
    assert.ok(Number.isNaN(result as number));
  });

  test('gauge mode keeps a negative final delta', () => {
    // 40 -> 5 is not a reset for gauges: delta -35 over 5s = -7/s.
    const samples = [s(0, 100), s(20000, 40), s(25000, 5)];
    assert.ok(closeTo(irate(samples, 25000, 25000, false) as number, -7));
  });
});
