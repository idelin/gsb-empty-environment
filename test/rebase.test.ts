import assert from 'node:assert/strict';
import test from 'node:test';
import { applyPatch } from '../src/model.ts';
import type { Patch } from '../src/model.ts';
import { rebase } from '../src/rebase.ts';

const patch = (id: string, edits: Array<[number, number, string]>): Patch => ({
  id,
  edits: edits.map(([start, end, text], i) => ({ id: `${id}#${i}`, start, end, text })),
});

test('applyPatch applies inserts, deletes and replacements', () => {
  const p = patch('p', [
    [0, 0, '>>'],
    [2, 3, 'X'],
    [4, 5, ''],
  ]);
  assert.equal(applyPatch('abcde', p), '>>abXd');
});

test('non-overlapping edits shift coordinates', () => {
  const a = patch('a', [[0, 1, 'LONG']]); // "xbc" -> "LONGbc"
  const b = patch('b', [[2, 3, 'Y']]); // replaces 'c'
  const r = rebase(a, b, 'conflict');
  assert.deepEqual(
    r.patch.edits.map((e) => [e.start, e.end, e.text]),
    [[5, 6, 'Y']],
  );
  assert.equal(applyPatch(applyPatch('xbc', a), r.patch), 'LONGbY');
  assert.equal(r.unpreserved.length, 0);
  assert.equal(r.conflicts.length, 0);
});

test('insertions at the same point are ordered by patch id', () => {
  const a = patch('alpha', [[1, 1, 'A']]);
  const b = patch('beta', [[1, 1, 'B']]);
  // alpha < beta, so alpha's text comes first regardless of rebase direction.
  const forward = rebase(a, b, 'conflict');
  const backward = rebase(b, a, 'conflict');
  assert.equal(applyPatch(applyPatch('xy', a), forward.patch), 'xABy');
  assert.equal(applyPatch(applyPatch('xy', b), backward.patch), 'xABy');
});

test('insertion at same point: larger id rebased onto smaller id goes last', () => {
  const small = patch('a-small', [[0, 0, '1']]);
  const large = patch('z-large', [[0, 0, '2']]);
  const r = rebase(small, large, 'conflict');
  assert.equal(applyPatch(applyPatch('', small), r.patch), '12');
  const r2 = rebase(large, small, 'conflict');
  assert.equal(applyPatch(applyPatch('', large), r2.patch), '12');
});

test('insertion at range boundaries does not conflict', () => {
  const a = patch('a', [[0, 0, 'i'], [2, 2, 'j']]);
  const b = patch('b', [[0, 2, 'R']]);
  const r = rebase(a, b, 'conflict');
  assert.equal(r.conflicts.length, 0);
  // 'i' lands before R, 'j' lands after R.
  assert.equal(applyPatch(applyPatch('xy', a), r.patch), 'iRj');
});

test('insertion strictly inside a replaced range conflicts', () => {
  const a = patch('a', [[1, 1, 'i']]);
  const b = patch('b', [[0, 2, 'R']]);
  const r = rebase(a, b, 'conflict');
  assert.equal(r.conflicts.length, 1);
  assert.equal(r.unpreserved.length, 1);
  assert.equal(r.patch.edits.length, 0);
});

test('overlapping ranges: conflict strategy reports and conserves', () => {
  const a = patch('a', [[0, 2, 'AA']]);
  const b = patch('b', [
    [1, 3, 'BB'],
    [4, 4, 'ins'],
  ]);
  const r = rebase(a, b, 'conflict');
  assert.equal(r.conflicts.length, 1);
  assert.equal(r.unpreserved.length, 1);
  assert.equal(r.unpreserved[0].edit.text, 'BB');
  assert.equal(r.patch.edits.length, 1); // the insertion survives
});

test('overlapping ranges: left-priority drops the right edit, keeps the rest', () => {
  const a = patch('a', [[0, 2, 'AA']]);
  const b = patch('b', [
    [1, 3, 'BB'],
    [3, 3, '!'],
  ]);
  const r = rebase(a, b, 'left-priority');
  assert.equal(r.conflicts.length, 0); // not a reported conflict under this strategy
  assert.equal(r.unpreserved.length, 1);
  assert.match(r.unpreserved[0].reason, /left patch takes priority/);
  assert.equal(r.patch.edits.length, 1);
  assert.equal(applyPatch(applyPatch('wxyz', a), r.patch), 'AAy!z');
});

test('provenance: rebased edits carry origin back to the source edit', () => {
  const a = patch('left', [[0, 1, 'LL']]);
  const b = patch('right', [
    [1, 2, 'x'],
    [3, 3, 'y'],
  ]);
  const r = rebase(a, b, 'conflict');
  assert.deepEqual(
    r.patch.edits.map((e) => e.origin),
    [
      { patchId: 'right', editId: 'right#0' },
      { patchId: 'right', editId: 'right#1' },
    ],
  );
});

test('no silent drops: kept + unpreserved always equals the original count', () => {
  const a = patch('a', [
    [0, 2, 'AA'],
    [3, 3, 'i'],
  ]);
  const b = patch('b', [
    [1, 2, 'x'],
    [2, 2, 'y'],
    [4, 5, 'z'],
  ]);
  for (const strategy of ['conflict', 'left-priority'] as const) {
    const r = rebase(a, b, strategy);
    assert.equal(r.patch.edits.length + r.unpreserved.length, b.edits.length);
  }
});

test('empty patches rebase to empty patches', () => {
  const r = rebase(patch('a', []), patch('b', []), 'conflict');
  assert.equal(r.patch.edits.length, 0);
  assert.equal(r.unpreserved.length, 0);
});
