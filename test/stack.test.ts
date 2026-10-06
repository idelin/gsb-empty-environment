import assert from 'node:assert/strict';
import { test } from 'node:test';
import { stackSeries } from '../src/stack.ts';
import type { SeriesInput } from '../src/types.ts';

const a: SeriesInput = {
  id: 'a',
  points: [
    { time: 1, value: 2 },
    { time: 2, value: 0 },
    { time: 3, value: -4 },
  ],
};
const b: SeriesInput = {
  id: 'b',
  points: [
    { time: 1, value: 3 },
    { time: 2, value: null },
    { time: 4, value: 5 },
  ],
};

test('aligns series on the union of timestamps, sorted', () => {
  const res = stackSeries([a, b], { mode: 'absolute' });
  assert.deepEqual(res.times, [1, 2, 3, 4]);
  assert.deepEqual(res.seriesIds, ['a', 'b']);
});

test('distinguishes missing (null / absent) from zero', () => {
  const res = stackSeries([a, b], { mode: 'absolute' });
  const t2 = res.columns[1];
  const pa = t2.points[0];
  const pb = t2.points[1];
  assert.equal(pa.missing, false, 'explicit zero is not missing');
  assert.equal(pa.value, 0);
  assert.equal(pb.missing, true, 'explicit null is missing');
  assert.equal(pb.value, null);
  const t4 = res.columns[3];
  assert.equal(t4.points[0].missing, true, 'absent point is missing');
  assert.equal(t4.points[1].missing, false);
});

test('positive values accumulate upward from zero in series order', () => {
  const res = stackSeries([a, b], { mode: 'absolute' });
  const [pa, pb] = res.columns[0].points;
  assert.deepEqual([pa.lower, pa.upper], [0, 2]);
  assert.deepEqual([pb.lower, pb.upper], [2, 5]);
  assert.equal(res.columns[0].posExtent, 5);
});

test('negative values accumulate downward from zero', () => {
  const res = stackSeries([a, b], { mode: 'absolute' });
  const t3 = res.columns[2];
  const [pa, pb] = t3.points;
  assert.deepEqual([pa.lower, pa.upper], [-4, 0]);
  assert.equal(pb.missing, true);
  assert.equal(t3.negExtent, -4);
  assert.equal(t3.posExtent, 0);
});

test('missing points occupy no layer and do not move the cursor', () => {
  const res = stackSeries([a, b], { mode: 'absolute' });
  const t4 = res.columns[3];
  const [pa, pb] = t4.points;
  assert.deepEqual([pa.lower, pa.upper], [0, 0]);
  assert.deepEqual([pb.lower, pb.upper], [0, 5], 'b stacks from zero, not from a');
});

test('zero value is a zero-height layer at the current cursor', () => {
  const top: SeriesInput = { id: 'top', points: [{ time: 1, value: 7 }] };
  const zero: SeriesInput = { id: 'zero', points: [{ time: 1, value: 0 }] };
  const res = stackSeries([top, zero], { mode: 'absolute' });
  const [pt, pz] = res.columns[0].points;
  assert.deepEqual([pt.lower, pt.upper], [0, 7]);
  assert.deepEqual([pz.lower, pz.upper], [7, 7]);
  assert.equal(pz.missing, false);
});

test('percent mode normalizes positive and negative sides independently', () => {
  const pos1: SeriesInput = { id: 'p1', points: [{ time: 1, value: 1 }] };
  const pos2: SeriesInput = { id: 'p2', points: [{ time: 1, value: 3 }] };
  const neg1: SeriesInput = { id: 'n1', points: [{ time: 1, value: -2 }] };
  const neg2: SeriesInput = { id: 'n2', points: [{ time: 1, value: -6 }] };
  const res = stackSeries([pos1, pos2, neg1, neg2], { mode: 'percent' });
  const [p1, p2, n1, n2] = res.columns[0].points;
  assert.ok(Math.abs(p1.upper - 0.25) < 1e-12);
  assert.ok(Math.abs(p2.lower - 0.25) < 1e-12 && Math.abs(p2.upper - 1) < 1e-12);
  assert.ok(Math.abs(n1.lower - -0.25) < 1e-12 && n1.upper === 0);
  assert.ok(Math.abs(n2.lower - -1) < 1e-12 && Math.abs(n2.upper - -0.25) < 1e-12);
});

test('percent mode with a zero total on one side leaves that side flat', () => {
  const pos: SeriesInput = { id: 'p', points: [{ time: 1, value: 4 }] };
  const zero: SeriesInput = { id: 'z', points: [{ time: 1, value: 0 }] };
  const res = stackSeries([pos, zero], { mode: 'percent' });
  const [pp, pz] = res.columns[0].points;
  assert.deepEqual([pp.lower, pp.upper], [0, 1]);
  assert.deepEqual([pz.lower, pz.upper], [1, 1]);
});

test('hidden series are excluded as if removed', () => {
  const res = stackSeries([a, b], { mode: 'absolute', hidden: new Set(['a']) });
  assert.deepEqual(res.seriesIds, ['b']);
  const t1 = res.columns[0];
  assert.equal(t1.points.length, 1);
  assert.deepEqual([t1.points[0].lower, t1.points[0].upper], [0, 3]);
});

test('string timestamps align lexicographically', () => {
  const s1: SeriesInput = { id: 's1', points: [{ time: '2024-02', value: 1 }] };
  const s2: SeriesInput = { id: 's2', points: [{ time: '2024-01', value: 2 }] };
  const res = stackSeries([s1, s2], { mode: 'absolute' });
  assert.deepEqual(res.times, ['2024-01', '2024-02']);
});
