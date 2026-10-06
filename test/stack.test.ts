import {
  StackResult,
  columnExtents,
  stackSeries,
} from '../src/stack';
import {
  checkAllZeroColumn,
  checkHiddenConservation,
  checkOrderConservation,
  makeSeries,
  rotateOrder,
} from '../src/checks';
import { closeTo, eq, ok, test } from './harness';

function cellAt(r: StackResult, id: string, t: number) {
  const s = r.series.find((x) => x.id === id);
  ok(s !== undefined, `series ${id} exists`);
  const col = r.times.indexOf(t);
  ok(col >= 0, `t=${t} on axis`);
  return (s as NonNullable<typeof s>).points[col];
}

test('aligns series on the union of timestamps', () => {
  const r = stackSeries(
    [
      makeSeries('a', [{ t: 2, v: 1 }, { t: 3, v: 2 }]),
      makeSeries('b', [{ t: 1, v: 5 }, { t: 3, v: 7 }]),
    ],
    'absolute'
  );
  eq(r.times, [1, 2, 3], 'sorted union of times');
});

test('distinguishes missing from explicit zero', () => {
  const r = stackSeries(
    [
      makeSeries('a', [{ t: 1, v: 0 }]),
      makeSeries('b', []),
    ],
    'absolute'
  );
  const a = cellAt(r, 'a', 1);
  const b = cellAt(r, 'b', 1);
  ok(!a.missing && a.zero, 'explicit 0 is zero, not missing');
  eq([a.lower, a.upper], [0, 0], 'zero parks on the baseline');
  ok(b.missing && !b.zero, 'absent point is missing, not zero');
  eq([b.lower, b.upper], [null, null], 'missing has null bounds');
});

test('positive and negative values accumulate from zero in both directions', () => {
  const r = stackSeries(
    [
      makeSeries('p1', [{ t: 1, v: 2 }]),
      makeSeries('n1', [{ t: 1, v: -3 }]),
      makeSeries('p2', [{ t: 1, v: 4 }]),
      makeSeries('n2', [{ t: 1, v: -1 }]),
    ],
    'absolute'
  );
  eq([cellAt(r, 'p1', 1).lower, cellAt(r, 'p1', 1).upper], [0, 2], 'p1 from baseline');
  eq([cellAt(r, 'p2', 1).lower, cellAt(r, 'p2', 1).upper], [2, 6], 'p2 stacks above p1');
  eq([cellAt(r, 'n1', 1).lower, cellAt(r, 'n1', 1).upper], [-3, 0], 'n1 below baseline');
  eq([cellAt(r, 'n2', 1).lower, cellAt(r, 'n2', 1).upper], [-4, -3], 'n2 stacks below n1');
  const ext = columnExtents(r.series.map((s) => s.points[0]));
  eq([ext.bottom, ext.top], [-4, 6], 'column extents');
});

test('percent mode normalizes each sign by its own absolute sum', () => {
  const r = stackSeries(
    [
      makeSeries('p1', [{ t: 1, v: 1 }]),
      makeSeries('p2', [{ t: 1, v: 3 }]),
      makeSeries('n1', [{ t: 1, v: -1 }]),
      makeSeries('n2', [{ t: 1, v: -1 }]),
    ],
    'percent'
  );
  closeTo(cellAt(r, 'p1', 1).upper as number, 0.25, 'p1 share');
  closeTo(cellAt(r, 'p2', 1).upper as number, 1.0, 'positive side spans [0,1]');
  closeTo(cellAt(r, 'n2', 1).lower as number, -1.0, 'negative side spans [0,-1]');
  closeTo(cellAt(r, 'n1', 1).lower as number, -0.5, 'n1 share');
});

test('percent mode keeps missing and zero distinct', () => {
  const r = stackSeries(
    [
      makeSeries('a', [{ t: 1, v: 0 }, { t: 2, v: 5 }]),
      makeSeries('b', [{ t: 2, v: 5 }]),
    ],
    'percent'
  );
  const a1 = cellAt(r, 'a', 1);
  const b1 = cellAt(r, 'b', 1);
  ok(a1.zero && !a1.missing, 'zero stays zero in percent mode');
  eq([a1.lower, a1.upper], [0, 0], 'zero has no height in percent mode');
  ok(b1.missing, 'missing stays missing in percent mode');
});

test('conservation: all-zero column', () => {
  const inputs = [
    makeSeries('a', [{ t: 1, v: 0 }, { t: 2, v: 3 }]),
    makeSeries('b', [{ t: 1, v: 0 }, { t: 2, v: -2 }]),
    makeSeries('c', [{ t: 1, v: 0 }]),
  ];
  for (const mode of ['absolute', 'percent'] as const) {
    const r = stackSeries(inputs, mode);
    eq(checkAllZeroColumn(r, 1), [], `all-zero column holds in ${mode} mode`);
  }
});

test('conservation: series order switching preserves heights and extents', () => {
  const inputs = [
    makeSeries('a', [{ t: 1, v: 2 }, { t: 2, v: -1 }, { t: 3, v: 0 }]),
    makeSeries('b', [{ t: 1, v: -4 }, { t: 2, v: 6 }]),
    makeSeries('c', [{ t: 2, v: 1 }, { t: 3, v: -5 }]),
    makeSeries('d', [{ t: 1, v: 3 }, { t: 3, v: 2 }]),
  ];
  for (const mode of ['absolute', 'percent'] as const) {
    for (let k = 1; k < inputs.length; k++) {
      const v = checkOrderConservation(inputs, rotateOrder(inputs.length, k), mode);
      eq(v, [], `order conservation (${mode}, rotation ${k})`);
    }
    const reversed = inputs.map((_, i) => inputs.length - 1 - i);
    eq(checkOrderConservation(inputs, reversed, mode), [], `order conservation (${mode}, reversed)`);
  }
});

test('conservation: hidden series conserve visible slice heights', () => {
  const inputs = [
    makeSeries('a', [{ t: 1, v: 2 }, { t: 2, v: -1 }]),
    makeSeries('b', [{ t: 1, v: -4 }, { t: 2, v: 6 }]),
    makeSeries('c', [{ t: 1, v: 3 }, { t: 2, v: 2 }]),
  ];
  for (const mode of ['absolute', 'percent'] as const) {
    for (const id of ['a', 'b', 'c']) {
      eq(checkHiddenConservation(inputs, id, mode), [], `hidden conservation (${mode}, hide ${id})`);
    }
  }
});

test('hidden series keep the time axis but contribute nothing', () => {
  const r = stackSeries(
    [
      makeSeries('a', [{ t: 1, v: 2 }]),
      makeSeries('ghost', [{ t: 0, v: 100 }, { t: 2, v: 100 }], true),
    ],
    'absolute'
  );
  eq(r.times, [0, 1, 2], 'hidden series still extend the axis');
  const ghost = r.series.find((s) => s.id === 'ghost');
  ok(ghost !== undefined && ghost.hidden, 'ghost flagged hidden');
  ok((ghost as NonNullable<typeof ghost>).points.every((p) => p.missing), 'ghost points are inert');
  const a1 = cellAt(r, 'a', 1);
  eq([a1.lower, a1.upper], [0, 2], 'visible stack unaffected by hidden values');
});
