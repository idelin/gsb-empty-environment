import { test } from 'node:test';
import assert from 'node:assert/strict';
import { largestRemainder, scaledDeviation, scaleWeights, solveApportion } from '../src/index.ts';

function baselineOf(ids: string[], weights: string[], total: bigint) {
  const scaled = scaleWeights(ids.map((id, i) => ({ id, weight: { num: BigInt(weights[i].split('/')[0]), den: BigInt(weights[i].split('/')[1] ?? '1') } })));
  const values = largestRemainder(scaled.ids, scaled.numerators, scaled.scale, total);
  const out: Record<string, bigint> = {};
  scaled.ids.forEach((id, i) => (out[id] = values[i]));
  return out;
}

test('total 1 split three ways: exactly one unit allocated, to the stable smallest id', () => {
  const alloc = baselineOf(['a', 'b', 'c'], ['1/3', '1/3', '1/3'], 1n);
  assert.deepEqual(alloc, { a: 1n, b: 0n, c: 0n });
  assert.equal(alloc.a + alloc.b + alloc.c, 1n);
});

test('tie-break is by stable id, not input order', () => {
  const alloc = baselineOf(['c', 'b', 'a'], ['1/3', '1/3', '1/3'], 1n);
  assert.deepEqual(alloc, { a: 1n, b: 0n, c: 0n });
});

test('classic 10 seats, equal thirds -> 4/3/3 by id order', () => {
  const alloc = baselineOf(['a', 'b', 'c'], ['1/3', '1/3', '1/3'], 10n);
  assert.deepEqual(alloc, { a: 4n, b: 3n, c: 3n });
});

test('conservation holds for huge totals (BigInt only)', () => {
  const total = 10n ** 24n + 1n;
  const alloc = baselineOf(['a', 'b', 'c'], ['1/3', '1/3', '1/3'], total);
  assert.equal(alloc.a + alloc.b + alloc.c, total);
  const f = (total - 2n) / 3n; // total mod 3 == 2
  assert.deepEqual(alloc, { a: f + 1n, b: f + 1n, c: f });
});

test('exact decimal weights at 10^27 scale, zero remainder', () => {
  const result = solveApportion({
    total: 10n ** 27n,
    items: [
      { id: 'x', weight: '0.1' },
      { id: 'y', weight: '0.2' },
      { id: 'z', weight: '0.7' },
    ],
  });
  assert.equal(result.status, 'ok');
  if (result.status !== 'ok') return;
  assert.deepEqual(result.allocation, {
    x: 10n ** 26n,
    y: 2n * 10n ** 26n,
    z: 7n * 10n ** 26n,
  });
  assert.equal(result.deviation, 0n);
});

test('negative weight is rejected', () => {
  assert.throws(
    () => solveApportion({ total: 1n, items: [{ id: 'a', weight: '-0.5' }, { id: 'b', weight: 1n }] }),
    /negative weight rejected/,
  );
});

test('all-zero weights are rejected', () => {
  assert.throws(
    () => solveApportion({ total: 1n, items: [{ id: 'a', weight: 0n }, { id: 'b', weight: '0.0' }] }),
    /total weight must be positive/,
  );
});

test('duplicate ids and negative totals are rejected', () => {
  assert.throws(
    () => solveApportion({ total: 1n, items: [{ id: 'a', weight: 1n }, { id: 'a', weight: 1n }] }),
    /duplicate id/,
  );
  assert.throws(() => solveApportion({ total: -1n, items: [{ id: 'a', weight: 1n }] }), /non-negative/);
});

test('scaledDeviation matches hand computation', () => {
  // {4,3,3} with N=[1,1,1], S=3, T=10 -> |12-10|+|9-10|+|9-10| = 4
  assert.equal(scaledDeviation([4n, 3n, 3n], [1n, 1n, 1n], 3n, 10n), 4n);
});
