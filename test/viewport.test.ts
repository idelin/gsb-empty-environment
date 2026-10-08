import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TimeSeries } from '../src/series.ts';
import { downsampleFull } from '../src/downsample.ts';
import { queryViewport } from '../src/viewport.ts';
import { verifyEnvelope } from '../src/envelope.ts';
import { computeSegments } from '../src/segments.ts';
import { makeSeries, mulberry32, randomViewport } from './helpers.ts';

test('summary-tree viewport query equals full downsampling, point for point', () => {
  const rng = mulberry32(7);
  for (let iter = 0; iter < 60; iter++) {
    const n = 1 + Math.floor(rng() * 400);
    const { times, values } = makeSeries(rng, n);
    const s = new TimeSeries(times, values);
    const { t0, t1, width } = randomViewport(rng, times);
    const expected = downsampleFull(s, t0, t1, width);
    const actual = queryViewport(s, t0, t1, width);
    assert.deepEqual(actual, expected, `iter ${iter} n=${n} vp=[${t0},${t1}] w=${width}`);
  }
});

test('equivalence holds after local edits that move extrema and breakpoints', () => {
  const rng = mulberry32(99);
  const { times, values } = makeSeries(rng, 600);
  const s = new TimeSeries(times, values);
  for (let round = 0; round < 25; round++) {
    // Local edits: flip points between valid/missing and move values around.
    for (let k = 0; k < 12; k++) {
      const i = Math.floor(rng() * s.length);
      const r = rng();
      if (r < 0.4) s.setValue(i, null);
      else if (r < 0.7) s.setValue(i, (rng() - 0.5) * 1e6);
      else s.setValue(i, (rng() - 0.5) * 10);
    }
    if (rng() < 0.5) {
      const start = Math.floor(rng() * (s.length - 20));
      const run = Array.from({ length: 20 }, () => (rng() < 0.4 ? null : rng() * 100));
      s.setValues(start, run);
    }
    // Segments must match a from-scratch recomputation.
    const fresh = computeSegments((i) => s.isValid(i), s.length);
    assert.deepEqual(
      s.segments.map((g) => [g.start, g.end]),
      fresh.map((g) => [g.start, g.end]),
      `segments diverged at round ${round}`,
    );
    // Tree path must still equal the flat reference on a random viewport.
    const { t0, t1, width } = randomViewport(rng, times);
    assert.deepEqual(queryViewport(s, t0, t1, width), downsampleFull(s, t0, t1, width));
  }
});

test('every output point carries containing evidence ranges inside its segment', () => {
  const rng = mulberry32(1234);
  const { times, values } = makeSeries(rng, 300);
  const s = new TimeSeries(times, values);
  const { t0, t1, width } = randomViewport(rng, times);
  for (const p of queryViewport(s, t0, t1, width)) {
    const seg = s.segmentAt(p.index)!;
    assert.ok(p.rangeLo <= p.index && p.index <= p.rangeHi, `point ${p.index}`);
    assert.ok(p.rangeLo >= seg.start && p.rangeHi <= seg.end, `evidence of ${p.index}`);
    // Bucket evidence (non-boundary) never crosses the viewport on the inside:
    // every raw index in the evidence range is valid (same segment).
    for (let i = p.rangeLo; i <= p.rangeHi; i++) assert.ok(s.isValid(i));
  }
});

test('boundary neighbours are the nearest in-segment points outside the viewport', () => {
  const rng = mulberry32(555);
  for (let iter = 0; iter < 40; iter++) {
    const { times, values } = makeSeries(rng, 200);
    const s = new TimeSeries(times, values);
    const { t0, t1, width } = randomViewport(rng, times);
    const out = queryViewport(s, t0, t1, width);
    for (const p of out) {
      if (p.roles.includes('boundary-left')) {
        assert.ok(p.time < t0);
        const seg = s.segmentAt(p.index)!;
        assert.ok(p.index + 1 > seg.end || s.times[p.index + 1] >= t0 || !s.isValid(p.index + 1));
      }
      if (p.roles.includes('boundary-right')) {
        assert.ok(p.time > t1);
        const seg = s.segmentAt(p.index)!;
        assert.ok(p.index - 1 < seg.start || s.times[p.index - 1] <= t1 || !s.isValid(p.index - 1));
      }
    }
    // Interior points never come from outside the viewport.
    for (const p of out) {
      if (!p.roles.includes('boundary-left') && !p.roles.includes('boundary-right')) {
        assert.ok(p.time >= t0 && p.time <= t1, `interior point ${p.index} outside viewport`);
      }
    }
  }
});

test('clean outputs pass the envelope check on randomized data', () => {
  const rng = mulberry32(2024);
  for (let iter = 0; iter < 40; iter++) {
    const { times, values } = makeSeries(rng, 50 + Math.floor(rng() * 300));
    const s = new TimeSeries(times, values);
    const { t0, t1, width } = randomViewport(rng, times);
    const out = queryViewport(s, t0, t1, width);
    assert.deepEqual(verifyEnvelope(s, out, t0, t1, width), [], `iter ${iter}`);
  }
});
