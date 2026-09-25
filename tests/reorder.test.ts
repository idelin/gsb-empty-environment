import { test } from 'node:test';
import assert from 'node:assert/strict';

import { allLevelSequences, reorderByLevels, reorderReference } from '../src/reorder.ts';

test('hand-computed canonical nested case: levels 0 0 1 1 2 2 1 0', () => {
  const levels = [0, 0, 1, 1, 2, 2, 1, 0];
  // logical text: a b H E X Y I f -> visual: a b I X Y E H f
  assert.deepEqual(reorderByLevels(levels), [0, 1, 6, 4, 5, 3, 2, 7]);
  assert.deepEqual(reorderReference(levels), [0, 1, 6, 4, 5, 3, 2, 7]);
});

test('uniform levels keep logical order (even) / fully reverse (odd)', () => {
  assert.deepEqual(reorderByLevels([0, 0, 0, 0]), [0, 1, 2, 3]);
  assert.deepEqual(reorderByLevels([1, 1, 1, 1]), [3, 2, 1, 0]);
  assert.deepEqual(reorderByLevels([2, 2, 2, 2]), [0, 1, 2, 3]);
});

test('independent per-segment reference agrees on exhaustive small level sequences', () => {
  let checked = 0;
  for (let n = 0; n <= 6; n++) {
    for (const seq of allLevelSequences(n, 3)) {
      assert.deepEqual(reorderByLevels(seq), reorderReference(seq), `sequence ${seq.join(',')}`);
      checked++;
    }
  }
  // 1 + 4 + 16 + 64 + 256 + 1024 + 4096
  assert.equal(checked, 5461);
});

test('every result is a valid permutation of logical indices (exhaustive)', () => {
  for (const seq of allLevelSequences(5, 3)) {
    const order = reorderByLevels(seq);
    assert.equal(order.length, seq.length);
    assert.deepEqual([...order].sort((a, b) => a - b), seq.map((_, i) => i));
  }
});

test('round trip: logical->visual->logical is identity on every fragment', () => {
  for (const seq of allLevelSequences(5, 3)) {
    const visualOrder = reorderByLevels(seq);
    const logicalAtVisual = visualOrder.map((logicalIndex, visualIndex) => ({ visualIndex, logicalIndex }));
    for (let logicalIndex = 0; logicalIndex < seq.length; logicalIndex++) {
      const visualIndex = visualOrder.indexOf(logicalIndex);
      assert.equal(logicalAtVisual[visualIndex]!.logicalIndex, logicalIndex);
    }
  }
});

test('multi-valued boundaries stay multi-valued: nested run contributes both sides', () => {
  // levels 0 | 1 1 | 2 2 | 1 : offset boundaries are not unique visual points
  const levels = [0, 1, 1, 2, 2, 1];
  const order = reorderByLevels(levels);
  assert.deepEqual(order, [0, 5, 3, 4, 2, 1]);
});
