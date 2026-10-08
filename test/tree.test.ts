import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SummaryTree } from '../src/tree.ts';
import { mulberry32 } from './helpers.ts';

function brute(values: (number | null)[], lo: number, hi: number) {
  let count = 0;
  let firstIdx = -1;
  let lastIdx = -1;
  let minIdx = -1;
  let maxIdx = -1;
  for (let i = lo; i <= hi; i++) {
    const v = values[i];
    if (v === null || Number.isNaN(v)) continue;
    count++;
    if (firstIdx < 0) firstIdx = i;
    lastIdx = i;
    if (minIdx < 0 || v < (values[minIdx] as number)) minIdx = i;
    if (maxIdx < 0 || v > (values[maxIdx] as number)) maxIdx = i;
  }
  return { count, firstIdx, lastIdx, minIdx, maxIdx };
}

test('range queries match brute force, ties keep the smaller index', () => {
  const rng = mulberry32(42);
  const n = 500;
  const values: (number | null)[] = [];
  for (let i = 0; i < n; i++) {
    const r = rng();
    values.push(r < 0.2 ? null : Math.floor(rng() * 7)); // few distinct values -> many ties
  }
  const tree = new SummaryTree(values);
  for (let k = 0; k < 300; k++) {
    const lo = Math.floor(rng() * n);
    const hi = lo + Math.floor(rng() * (n - lo));
    assert.deepEqual(tree.query(lo, hi), brute(values, lo, hi), `range [${lo}, ${hi}]`);
  }
});

test('updates re-propagate extrema and first/last', () => {
  const values: (number | null)[] = [1, 5, null, 3, 2];
  const tree = new SummaryTree(values);
  assert.equal(tree.query(0, 4).maxIdx, 1);
  values[1] = null;
  tree.update(1);
  assert.deepEqual(tree.query(0, 4), brute(values, 0, 4));
  values[2] = 99;
  tree.update(2);
  assert.deepEqual(tree.query(0, 4), brute(values, 0, 4));
  assert.equal(tree.query(0, 4).maxIdx, 2);
  assert.equal(tree.query(0, 4).firstIdx, 0);
  assert.equal(tree.query(0, 4).lastIdx, 4);
});
