import assert from 'node:assert/strict';
import { test } from 'node:test';
import {
  checkAllZeroColumn,
  checkHiddenEquivalence,
  checkOrderInvariance,
  checkPercentConservation,
} from '../src/conservation.ts';
import { stackSeries } from '../src/stack.ts';
import type { SeriesInput, StackMode } from '../src/types.ts';

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

function randomSeries(rng: () => number, id: string, times: number[]): SeriesInput {
  const points = times
    .filter(() => rng() < 0.8)
    .map((time) => {
      const r = rng();
      const value = r < 0.15 ? null : r < 0.3 ? 0 : Math.round(rng() * 40 - 20);
      return { time, value };
    });
  return { id, points };
}

const modes: StackMode[] = ['absolute', 'percent'];

test('all-zero column: every layer sits at the baseline in both modes', () => {
  const z1: SeriesInput = {
    id: 'z1',
    points: [
      { time: 1, value: 0 },
      { time: 2, value: 5 },
    ],
  };
  const z2: SeriesInput = {
    id: 'z2',
    points: [
      { time: 1, value: 0 },
      { time: 2, value: -3 },
    ],
  };
  const z3: SeriesInput = { id: 'z3', points: [{ time: 1, value: null }] };
  for (const mode of modes) {
    const res = stackSeries([z1, z2, z3], { mode });
    assert.ok(checkAllZeroColumn(res.columns[0]), `${mode}: zero column is flat`);
    assert.ok(!checkAllZeroColumn(res.columns[1]), `${mode}: non-zero column is not flat`);
  }
});

test('series order switching preserves extents and layer-height multiset', () => {
  const rng = mulberry32(42);
  const times = [1, 2, 3, 4, 5, 6];
  const series = ['a', 'b', 'c', 'd'].map((id) => randomSeries(rng, id, times));
  const permutations: string[][] = [
    ['a', 'b', 'c', 'd'],
    ['d', 'c', 'b', 'a'],
    ['b', 'd', 'a', 'c'],
    ['c', 'a', 'd', 'b'],
  ];
  for (const mode of modes) {
    const results = permutations.map((perm) => {
      const ordered = perm.map((id) => series.find((s) => s.id === id)!);
      return stackSeries(ordered, { mode });
    });
    for (let i = 1; i < results.length; i += 1) {
      const report = checkOrderInvariance(results[0], results[i]);
      assert.ok(report.ok, `${mode}: ${report.violations.join('; ')}`);
    }
  }
});

test('per-series layer height is invariant under series reordering', () => {
  const rng = mulberry32(7);
  const times = [1, 2, 3, 4];
  const series = ['a', 'b', 'c'].map((id) => randomSeries(rng, id, times));
  for (const mode of modes) {
    const forward = stackSeries(series, { mode });
    const backward = stackSeries([...series].reverse(), { mode });
    for (let c = 0; c < forward.columns.length; c += 1) {
      for (const point of forward.columns[c].points) {
        const match = backward.columns[c].points.find(
          (p) => p.seriesId === point.seriesId,
        )!;
        const h1 = point.upper - point.lower;
        const h2 = match.upper - match.lower;
        assert.ok(Math.abs(h1 - h2) < 1e-9, `${mode} ${point.seriesId}@${c}`);
      }
    }
  }
});

test('hidden series equals removed series', () => {
  const rng = mulberry32(99);
  const times = [1, 2, 3, 4, 5];
  const series = ['a', 'b', 'c', 'd'].map((id) => randomSeries(rng, id, times));
  for (const mode of modes) {
    for (const hiddenIds of [['b'], ['a', 'd'], ['c']]) {
      assert.ok(
        checkHiddenEquivalence(series, hiddenIds, mode),
        `${mode} hidden=[${hiddenIds}]`,
      );
    }
  }
});

test('percent conservation holds on randomized data', () => {
  const rng = mulberry32(2024);
  const times = [1, 2, 3, 4, 5, 6, 7, 8];
  const series = ['a', 'b', 'c', 'd', 'e'].map((id) => randomSeries(rng, id, times));
  const res = stackSeries(series, { mode: 'percent' });
  assert.deepEqual(checkPercentConservation(res), []);
});
