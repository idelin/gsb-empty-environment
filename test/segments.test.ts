import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TimeSeries } from '../src/series.ts';
import { computeSegments } from '../src/segments.ts';

const segs = (s: TimeSeries): Array<[number, number]> => s.segments.map((g) => [g.start, g.end]);

test('computeSegments splits at missing values', () => {
  const valid = [true, true, false, true, false, false, true, true, true];
  const out = computeSegments((i) => valid[i], valid.length);
  assert.deepEqual(out.map((s) => [s.start, s.end]), [[0, 1], [3, 3], [6, 8]]);
});

test('NaN and undefined count as missing', () => {
  const s = new TimeSeries([0, 1, 2, 3], [1, NaN, undefined as unknown as null, 4]);
  assert.deepEqual(segs(s), [[0, 0], [3, 3]]);
});

test('setValue to missing splits a segment', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4], [1, 2, 3, 4, 5]);
  assert.deepEqual(segs(s), [[0, 4]]);
  s.setValue(2, null);
  assert.deepEqual(segs(s), [[0, 1], [3, 4]]);
  assert.equal(s.version, 1);
});

test('setValue back to valid merges segments', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4], [1, null, 3, null, 5]);
  assert.deepEqual(segs(s), [[0, 0], [2, 2], [4, 4]]);
  s.setValue(1, 9);
  assert.deepEqual(segs(s), [[0, 2], [4, 4]]);
  s.setValue(3, 8);
  assert.deepEqual(segs(s), [[0, 4]]);
});

test('setValues repairs a whole window with one version bump', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4, 5], [1, 2, 3, 4, 5, 6]);
  s.setValues(1, [null, null, null]);
  assert.deepEqual(segs(s), [[0, 0], [4, 5]]);
  assert.equal(s.version, 1);
  s.setValues(2, [7, 8]);
  assert.deepEqual(segs(s), [[0, 0], [2, 5]]);
  assert.equal(s.version, 2);
});

test('no-op writes do not bump the version', () => {
  const s = new TimeSeries([0, 1], [1, null]);
  s.setValue(0, 1);
  s.setValue(1, NaN);
  assert.equal(s.version, 0);
});

test('segment ids are unique across the current segment list', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4], [1, null, 3, null, 5]);
  const ids = s.segments.map((g) => g.id);
  assert.equal(new Set(ids).size, ids.length);
  s.setValue(1, 2);
  const ids2 = s.segments.map((g) => g.id);
  assert.equal(new Set(ids2).size, ids2.length);
});
