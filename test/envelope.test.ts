import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TimeSeries } from '../src/series.ts';
import { queryViewport } from '../src/viewport.ts';
import { verifyEnvelope } from '../src/envelope.ts';
import type { SampledPoint } from '../src/types.ts';

const kinds = (vs: Array<{ kind: string }>) => new Set(vs.map((v) => v.kind));

test('detects a dropped spike (column maximum removed)', () => {
  const times = Array.from({ length: 40 }, (_, i) => i);
  const values = times.map(() => 0);
  values[17] = 1e6;
  const s = new TimeSeries(times, values);
  const out = queryViewport(s, 0, 39, 4);
  const corrupted = out.filter((p) => p.index !== 17);
  const violations = verifyEnvelope(s, corrupted, 0, 39, 4);
  assert.ok(kinds(violations).has('spike'));
});

test('detects a dropped column minimum', () => {
  const times = Array.from({ length: 40 }, (_, i) => i);
  const values = times.map(() => 5);
  values[23] = -1e6;
  const s = new TimeSeries(times, values);
  const out = queryViewport(s, 0, 39, 4);
  const corrupted = out.filter((p) => p.index !== 23);
  assert.ok(kinds(verifyEnvelope(s, corrupted, 0, 39, 4)).has('spike'));
});

test('detects out-of-order duplicate timestamps', () => {
  const s = new TimeSeries([1, 1, 1, 2], [3, 1, 2, 4]);
  const out = queryViewport(s, 0, 4, 2);
  const corrupted: SampledPoint[] = [...out];
  // Swap two adjacent points that share the same timestamp.
  const i = corrupted.findIndex((p, k) => k + 1 < corrupted.length && corrupted[k + 1].time === p.time);
  assert.ok(i >= 0);
  [corrupted[i], corrupted[i + 1]] = [corrupted[i + 1], corrupted[i]];
  const found = kinds(verifyEnvelope(s, corrupted, 0, 4, 2));
  assert.ok(found.has('order') || found.has('duplicate-time'));
});

test('detects a dropped single-point segment', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4, 5, 6], [1, 2, null, 42, null, 6, 7]);
  const out = queryViewport(s, 0, 6, 3);
  assert.ok(out.some((p) => p.index === 3));
  const corrupted = out.filter((p) => p.index !== 3);
  assert.ok(kinds(verifyEnvelope(s, corrupted, 0, 6, 3)).has('single-point-segment'));
});

test('detects segment ids that bridge a missing gap', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4], [1, 2, null, 4, 5]);
  const out = queryViewport(s, 0, 4, 4);
  const left = out.find((p) => p.index === 1)!;
  const right = out.find((p) => p.index === 3)!;
  assert.notEqual(left.segmentId, right.segmentId);
  const corrupted = out.map((p) => (p.index === 3 ? { ...p, segmentId: left.segmentId } : p));
  assert.ok(kinds(verifyEnvelope(s, corrupted, 0, 4, 4)).has('gap-bridged'));
});

test('detects broken identity and evidence ranges', () => {
  const s = new TimeSeries([0, 1, 2, 3], [4, 3, 2, 1]);
  const out = queryViewport(s, 0, 3, 2);
  const wrongValue = out.map((p, k) => (k === 0 ? { ...p, value: p.value + 1 } : p));
  assert.ok(kinds(verifyEnvelope(s, wrongValue, 0, 3, 2)).has('identity'));
  const wrongRange = out.map((p, k) => (k === 0 ? { ...p, rangeLo: p.index + 1 } : p));
  assert.ok(kinds(verifyEnvelope(s, wrongRange, 0, 3, 2)).has('evidence'));
});

test('a correct output passes with zero violations', () => {
  const s = new TimeSeries(
    [0, 1, 1, 2, 3, 4, 5, 5, 6],
    [1, 7, -7, null, 3, null, 9, -9, 4],
  );
  const out = queryViewport(s, 0, 6, 3);
  assert.deepEqual(verifyEnvelope(s, out, 0, 6, 3), []);
});
