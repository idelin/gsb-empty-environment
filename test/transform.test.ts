import * as assert from 'assert';
import {
  applyEdits,
  transformPosition,
  transformPositionThroughEdits,
  transformSelection,
  validateEdits,
} from '../src/index';
import { test } from './harness';

test('insert: affinity decides the side at the insertion point', () => {
  const edit = { start: 2, end: 2, text: 'XY' };
  assert.strictEqual(transformPosition(1, edit, 'left'), 1);
  assert.strictEqual(transformPosition(1, edit, 'right'), 1);
  assert.strictEqual(transformPosition(2, edit, 'left'), 2);
  assert.strictEqual(transformPosition(2, edit, 'right'), 4);
  assert.strictEqual(transformPosition(3, edit, 'left'), 5);
  assert.strictEqual(transformPosition(3, edit, 'right'), 5);
});

test('delete: covered anchor snaps to the deletion start', () => {
  const edit = { start: 2, end: 5, text: '' };
  assert.strictEqual(transformPosition(1, edit), 1);
  assert.strictEqual(transformPosition(2, edit), 2); // deletion start stays
  assert.strictEqual(transformPosition(3, edit), 2); // interior snaps
  assert.strictEqual(transformPosition(4, edit), 2);
  assert.strictEqual(transformPosition(5, edit), 2); // end boundary snaps
  assert.strictEqual(transformPosition(6, edit), 3); // shifts by removed length
});

test('replace: covered anchor snaps to the deletion start, before inserted text', () => {
  const edit = { start: 2, end: 5, text: 'Z' };
  assert.strictEqual(transformPosition(3, edit, 'right'), 2);
  assert.strictEqual(transformPosition(5, edit, 'right'), 2);
  assert.strictEqual(transformPosition(6, edit, 'right'), 4);
});

test('applyEdits: adjacent deletions merge correctly', () => {
  const text = 'abcdef';
  const edits = [
    { start: 1, end: 3, text: '' }, // remove "bc"
    { start: 3, end: 5, text: '' }, // remove "de" (adjacent)
  ];
  assert.strictEqual(applyEdits(text, edits), 'af');
  // Anchor inside the first deletion snaps to 1; inside the second, to 3 -> then to 1.
  assert.strictEqual(transformPositionThroughEdits(2, edits), 1);
  assert.strictEqual(transformPositionThroughEdits(4, edits), 1);
  assert.strictEqual(transformPositionThroughEdits(5, edits), 1);
  assert.strictEqual(transformPositionThroughEdits(6, edits), 2);
});

test('applyEdits: same-point insertions keep their given order', () => {
  const text = 'abcd';
  const edits = [
    { start: 2, end: 2, text: 'X' },
    { start: 2, end: 2, text: 'YZ' },
  ];
  assert.strictEqual(applyEdits(text, edits), 'abXYZcd');
  assert.strictEqual(transformPositionThroughEdits(2, edits, 'left'), 2);
  assert.strictEqual(transformPositionThroughEdits(2, edits, 'right'), 5);
});

test('applyEdits: insertion adjacent to a deletion is allowed', () => {
  const text = 'abcd';
  const edits = [
    { start: 1, end: 3, text: '' },
    { start: 3, end: 3, text: 'Z' },
  ];
  assert.strictEqual(applyEdits(text, edits), 'aZd');
});

test('validateEdits: rejects overlapping edits', () => {
  assert.throws(() => validateEdits([{ start: 1, end: 4, text: '' }, { start: 3, end: 5, text: '' }]));
  // Insertion strictly inside a deleted range overlaps.
  assert.throws(() => validateEdits([{ start: 1, end: 4, text: '' }, { start: 2, end: 2, text: 'x' }]));
  // Negative or inverted ranges are rejected.
  assert.throws(() => validateEdits([{ start: -1, end: 2, text: '' }]));
  assert.throws(() => validateEdits([{ start: 3, end: 2, text: '' }]));
  // Touching ranges and same-point insertions are fine.
  assert.doesNotThrow(() =>
    validateEdits([
      { start: 1, end: 2, text: '' },
      { start: 2, end: 3, text: '' },
      { start: 2, end: 2, text: 'x' },
      { start: 2, end: 2, text: 'y' },
    ]),
  );
});

test('transformSelection: transforms both ends', () => {
  const edits = [{ start: 0, end: 0, text: '>>' }];
  assert.deepStrictEqual(transformSelection({ start: 1, end: 3 }, edits), { start: 3, end: 5 });
});

test('transformSelection: insertion at a boundary is excluded by default', () => {
  const atStart = [{ start: 1, end: 1, text: 'X' }];
  const atEnd = [{ start: 3, end: 3, text: 'X' }];
  assert.deepStrictEqual(transformSelection({ start: 1, end: 3 }, atStart), { start: 2, end: 4 });
  assert.deepStrictEqual(transformSelection({ start: 1, end: 3 }, atEnd), { start: 1, end: 3 });
});

test('transformSelection: empty selection stays empty at an insertion point', () => {
  const edits = [{ start: 2, end: 2, text: 'XY' }];
  assert.deepStrictEqual(transformSelection({ start: 2, end: 2 }, edits), { start: 4, end: 4 });
});

test('transformSelection: selection inside a deletion collapses to the deletion start', () => {
  const edits = [{ start: 1, end: 5, text: '' }];
  assert.deepStrictEqual(transformSelection({ start: 2, end: 4 }, edits), { start: 1, end: 1 });
});
