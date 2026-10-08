import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TimeSeries } from '../src/series.ts';
import { downsampleFull } from '../src/downsample.ts';

const byIndex = (pts: Array<{ index: number }>) => new Map(pts.map((p) => [p.index, p]));

test('per-column first/last/min/max with a gap, in original time order', () => {
  // Columns for t0=0, t1=9, width=3 (dt=3): [0,3) [3,6) [6,9].
  // Missing index 4 splits the series into segments [0..3] and [5..9].
  const s = new TimeSeries(
    [0, 1, 2, 3, 4, 5, 6, 7, 8, 9],
    [0, 5, -3, 2, null, 1, 1, 9, -9, 4],
  );
  const out = downsampleFull(s, 0, 9, 3);
  // Missing point never emitted; every other index selected by some role.
  assert.deepEqual(out.map((p) => p.index), [0, 1, 2, 3, 5, 6, 7, 8, 9]);
  const m = byIndex(out);
  assert.deepEqual(m.get(0)!.roles, ['first']);
  assert.deepEqual(m.get(1)!.roles, ['max']);
  assert.deepEqual(m.get(2)!.roles, ['last', 'min']);
  assert.deepEqual(m.get(3)!.roles, ['first', 'last', 'min', 'max']); // alone in its column
  assert.deepEqual(m.get(5)!.roles, ['first', 'last', 'min', 'max']);
  assert.deepEqual(m.get(6)!.roles, ['first']);
  assert.deepEqual(m.get(7)!.roles, ['max']);
  assert.deepEqual(m.get(8)!.roles, ['min']);
  assert.deepEqual(m.get(9)!.roles, ['last']);
  // Evidence: bucket index ranges.
  assert.deepEqual([m.get(0)!.rangeLo, m.get(0)!.rangeHi], [0, 2]);
  assert.deepEqual([m.get(2)!.rangeLo, m.get(2)!.rangeHi], [0, 2]);
  assert.deepEqual([m.get(3)!.rangeLo, m.get(3)!.rangeHi], [3, 3]);
  assert.deepEqual([m.get(7)!.rangeLo, m.get(7)!.rangeHi], [6, 9]);
  // Gap integrity: points across the missing index carry different segment ids.
  assert.notEqual(m.get(3)!.segmentId, m.get(5)!.segmentId);
});

test('spikes survive: column min and max are always kept', () => {
  const times = Array.from({ length: 100 }, (_, i) => i);
  const values = times.map(() => 0);
  values[37] = 1e9; // huge positive spike inside one column
  values[61] = -1e9; // huge negative spike in another
  const s = new TimeSeries(times, values);
  const out = downsampleFull(s, 0, 99, 4);
  const m = byIndex(out);
  assert.ok(m.has(37), 'positive spike kept');
  assert.ok(m.has(61), 'negative spike kept');
  assert.deepEqual(m.get(37)!.roles, ['max']);
  assert.deepEqual(m.get(61)!.roles, ['min']);
});

test('duplicate timestamps are emitted in original index order', () => {
  const s = new TimeSeries([1, 1, 1, 2, 2, 3], [3, 1, 2, 0, 4, 5]);
  const out = downsampleFull(s, 0, 4, 2);
  for (let i = 1; i < out.length; i++) {
    const a = out[i - 1];
    const b = out[i];
    assert.ok(a.time < b.time || (a.time === b.time && a.index < b.index));
  }
  // The equal-time run [1,1,1] lands in one column; min/max/first/last kept.
  const m = byIndex(out);
  assert.ok(m.has(0)); // first & max of the t=1 run
  assert.ok(m.has(1)); // min of the t=1 run
  assert.ok(m.has(2)); // last of the t=1 run
});

test('single-point segments survive any zoom level', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4], [null, null, 42, null, null]);
  for (const width of [1, 2, 7, 100]) {
    const out = downsampleFull(s, 0, 4, width);
    assert.deepEqual(out.map((p) => p.index), [2]);
    assert.deepEqual(out[0].roles.sort(), ['first', 'last', 'max', 'min']);
  }
});

test('points exactly on viewport edges belong to the first/last column', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4], [10, 20, 30, 40, 50]);
  const out = downsampleFull(s, 1, 3, 2, false); // no boundary neighbours
  const m = byIndex(out);
  assert.ok(m.has(1), 't0 point included');
  assert.ok(m.has(3), 't1 point included');
  assert.ok(!m.has(0) && !m.has(4), 'outside points excluded');
});

test('boundary neighbours extend one point past each viewport edge', () => {
  const s = new TimeSeries([0, 1, 2, 3, 4, 5, 6, 7, 8, 9], [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
  const out = downsampleFull(s, 3, 6, 3);
  const m = byIndex(out);
  assert.deepEqual(m.get(2)!.roles, ['boundary-left']);
  assert.deepEqual(m.get(7)!.roles, ['boundary-right']);
  assert.deepEqual([m.get(2)!.rangeLo, m.get(2)!.rangeHi], [2, 2]);
  // Without neighbours requested, they disappear.
  const noBound = downsampleFull(s, 3, 6, 3, false);
  assert.ok(!byIndex(noBound).has(2) && !byIndex(noBound).has(7));
});

test('boundary neighbours respect gaps: nearest point stays in-segment', () => {
  // Gap at index 3..4; viewport starts at 5, so the left neighbour of the
  // second segment does not exist inside that segment.
  const s = new TimeSeries([0, 1, 2, 3, 4, 5, 6], [1, 2, null, null, null, 6, 7]);
  const out = downsampleFull(s, 5, 6, 2);
  assert.ok(!out.some((p) => p.roles.includes('boundary-left')));
  const segIds = new Set(out.map((p) => p.segmentId));
  assert.equal(segIds.size, 1);
});
