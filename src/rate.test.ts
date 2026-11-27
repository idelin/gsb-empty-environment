import { describe, test } from 'node:test';
import assert from 'node:assert/strict';
import { rate, increase } from './index.js';
import { s, closeTo } from './test-helpers.js';

describe('rate / increase basic extrapolation', () => {
  test('two full-coverage points: rate is the raw slope', () => {
    // Window 0..30000ms; points at the two edges, value 100 -> 130 (delta 30).
    // factor = 1, rate = 30/30 = 1/s.
    const samples = [s(0, 100), s(30000, 130)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 1));
    // Two points at both window edges leave no boundary gap and the average
    // interval equals the window, so the increase duration collapses to zero:
    // rangeSec + 0 - avgSec = 0, hence increase() is 0 in this edge geometry.
    assert.ok(closeTo(increase(samples, 30000, 30000) as number, 0));
  });

  test('interior points extrapolate to both window edges', () => {
    // Window 0..30000; points at 5000 (v=100) and 25000 (v=120).
    // sampled=avg=20000, threshold=22000; gaps of 5000 each stay full.
    // factor = (20000+5000+5000)/20000 = 1.5 -> rate = (20/20)*1.5 = 1.5/s.
    // increase duration = 30 + 10 - 20 = 20s -> increase = 1.5 * 20 = 30.
    const samples = [s(5000, 100), s(25000, 120)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 1.5));
    assert.ok(closeTo(increase(samples, 30000, 30000) as number, 30));
  });

  test('increase shares the extrapolated rate and applies the increase duration', () => {
    // rate() ignores the duration function (factor fn returns 1); increase()
    // multiplies by (rangeSec + gapSec - avgIntervalSec).
    const samples = [s(1000, 2), s(11000, 8), s(21000, 5), s(31000, 20)];
    const evalTs = 61000;
    const rangeMs = 60000;
    const r = rate(samples, evalTs, rangeMs) as number;
    const inc = increase(samples, evalTs, rangeMs) as number;
    // sampled 1000..31000 = 30000, avg = 10000, gaps dStart=1000 dEnd=30000.
    // dEnd (30000) >= 1.1*10000=11000 -> capped to avg/2 = 5000.
    // increase duration = 60 + (1000+5000)/1000 - 10 = 55s.
    assert.ok(closeTo(inc, r * 55));
  });
});

describe('multiple counter resets', () => {
  test('each drop is treated as a reset and accumulated as the absolute new value', () => {
    // Window 0..30000. Values: 100 -> 40 (reset, add 40) -> 20 (reset, add 20).
    // resultValue = 40 + 20 = 60 over 30000ms, factor 1 -> rate 2/s.
    // avg = 15000; increase duration = 30 + 0 - 15 = 15s -> increase 30.
    const samples = [s(0, 100), s(15000, 40), s(30000, 20)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 2));
    assert.ok(closeTo(increase(samples, 30000, 30000) as number, 30));
  });

  test('resets mixed with normal increments', () => {
    // 10 -> 15 (add 5); 15 -> 3 (reset, add 3); 3 -> 9 (add 6) => total 14.
    // factor 1 -> rate 14/30 ; increase duration = 30 - avg(10) = 20s.
    const samples = [s(0, 10), s(10000, 15), s(20000, 3), s(30000, 9)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 14 / 30));
    assert.ok(closeTo(increase(samples, 30000, 30000) as number, (14 / 30) * 20));
  });
});

describe('sparse sampling: 1.1 * avgInterval half-interval truncation', () => {
  test('a single edge beyond the threshold caps only that extrapolation', () => {
    // Window 0..80000; points at 10000 (v=10) and 30000 (v=30).
    // sampled=avg=20000, threshold=22000. dStart=10000 (full); dEnd=50000
    // (>= threshold -> avg/2 = 10000). factor=(20000+10000+10000)/20000=2.
    // slope = 20/20 = 1/s -> rate 2/s.
    const samples = [s(10000, 10), s(30000, 30)];
    const evalTs = 80000;
    const rangeMs = 80000;
    assert.ok(closeTo(rate(samples, evalTs, rangeMs) as number, 2));
  });

  test('both sides sparse: each capped to half avgInterval', () => {
    // Window 0..100000; points at 40000 (v=0) and 60000 (v=40).
    // sampled=avg=20000, threshold=22000; gaps 40000 each -> both capped 10000.
    // factor=(20000+10000+10000)/20000=2; slope=40/20=2/s -> rate 3/s.
    // increase duration = 100 + 20 - 20 = 90s -> increase = 3*90 = 270.
    const samples = [s(40000, 0), s(60000, 40)];
    const evalTs = 100000;
    const rangeMs = 100000;
    assert.ok(closeTo(rate(samples, evalTs, rangeMs) as number, 3));
    assert.ok(closeTo(increase(samples, evalTs, rangeMs) as number, 270));
  });

  test('gap exactly at 1.1*avgInterval is truncated (>= threshold)', () => {
    // Two points 20000 apart; window 0..42000 starts exactly 1.1*20000=22000
    // before the first point. dStart=22000 (>= threshold -> half=10000),
    // dEnd=0. factor=(20000+10000)/20000=1.5; slope=1/s -> rate 1.5/s.
    const samples = [s(22000, 10), s(42000, 30)];
    const evalTs = 42000;
    const rangeMs = 42000;
    assert.ok(closeTo(rate(samples, evalTs, rangeMs) as number, 1.5));
  });
});

describe('left-edge zero clamp for freshly started counters', () => {
  test('back-extrapolation that would cross zero is clamped', () => {
    // Window 0..30000. Points 15000 (v=3) and 30000 (v=9). resultValue=6.
    // avg=sampled=15000, threshold=16500; dStart=15000 full, dEnd=0.
    // zero-cross duration = 3/6*15000 = 7500 < 15000 -> dStart clamped 7500.
    // factor=(15000+7500)/15000=1.125; slope=6/15=0.4/s -> rate 0.6/s.
    // increase duration = 30 + 7.5 - 15 = 22.5s -> increase = 0.6*22.5 = 13.5.
    const samples = [s(15000, 3), s(30000, 9)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 0.6));
    assert.ok(closeTo(increase(samples, 30000, 30000) as number, 13.5));
  });

  test('no clamp when the slope reaches the left edge without crossing zero', () => {
    // v0=100 means plenty of headroom: the zero crossing is beyond the window.
    // Points 15000 (v=100), 30000 (v=106); dStart=15000 full.
    // factor=30000/15000=2; slope 6/15=0.4/s -> rate 0.8/s.
    const samples = [s(15000, 100), s(30000, 106)];
    assert.ok(closeTo(rate(samples, 30000, 30000) as number, 0.8));
  });
});

describe('gauges (isCounter=false)', () => {
  test('decreases are kept as negative deltas and the zero clamp is skipped', () => {
    // Gauge: 10 -> 4 (delta -6) -> 8 (delta +4) => sampled sum -2. The
    // counter-only zero clamp must NOT fire, so no clamping is applied.
    // factor 1, rate = -2/30/s.
    const samples = [s(0, 10), s(15000, 4), s(30000, 8)];
    assert.ok(closeTo(rate(samples, 30000, 30000, false) as number, -2 / 30));
    // increase duration = 30 - avg(15) = 15s -> (-2/30)*15 = -1.
    assert.ok(closeTo(increase(samples, 30000, 30000, false) as number, -1));
  });

  test('gauge with interior points extrapolates without zero clamping', () => {
    // Gauge 50 -> 20 (delta -30), points at 5000/25000 inside 0..30000.
    // factor 1.5: slope -30/20 = -1.5 -> rate -2.25/s; increase duration
    // = 30 + 10 - avg(20) = 20s -> increase = -2.25 * 20 = -45.
    const samples = [s(5000, 50), s(25000, 20)];
    assert.ok(closeTo(rate(samples, 30000, 30000, false) as number, -2.25));
    assert.ok(closeTo(increase(samples, 30000, 30000, false) as number, -45));
  });
});

describe('insufficient samples', () => {
  test('fewer than two non-stale samples yields undefined', () => {
    assert.equal(rate([s(0, 1)], 30000, 30000), undefined);
    assert.equal(increase([], 30000, 30000), undefined);
  });

  test('samples outside the window are ignored', () => {
    // Only one point inside [0,30000]; the other is before the window.
    const samples = [s(-5000, 0), s(15000, 5)];
    assert.equal(rate(samples, 30000, 30000), undefined);
  });

  test('non-positive window yields undefined', () => {
    const samples = [s(0, 1), s(10, 2)];
    assert.equal(rate(samples, 10, 0), undefined);
  });
});
