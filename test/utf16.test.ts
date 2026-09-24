import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  codePointLength,
  hasLoneSurrogates,
  snapPoint,
  snapRange,
  splitsSurrogatePair,
} from '../src/utf16.ts';
import { applyEdit, editsConflict, transformRange } from '../src/rules.ts';

const THUMBS = '👍'; // 👍, one code point, two UTF-16 code units

test('surrogate pair occupies two UTF-16 offsets', () => {
  const text = `a${THUMBS}b`;
  assert.equal(text.length, 4);
  assert.equal(codePointLength(text), 3);
  assert.equal(splitsSurrogatePair(text, 2), true);
  assert.equal(splitsSurrogatePair(text, 1), false);
  assert.equal(splitsSurrogatePair(text, 3), false);
});

test('snapPoint never splits a surrogate pair (moves left)', () => {
  const text = `a${THUMBS}b`;
  assert.equal(snapPoint(text, 2), 1);
  assert.equal(snapPoint(text, 1), 1);
  assert.equal(snapPoint(text, 3), 3);
  assert.equal(snapPoint(text, -5), 0);
  assert.equal(snapPoint(text, 99), 4);
});

test('snapRange expands outward to whole code points', () => {
  const text = `a${THUMBS}b`;
  assert.deepEqual(snapRange(text, 2, 2), { start: 1, end: 1 });
  assert.deepEqual(snapRange(text, 1, 2), { start: 1, end: 3 });
  assert.deepEqual(snapRange(text, 2, 3), { start: 1, end: 3 });
});

test('applyEdit never splits a surrogate pair', () => {
  const text = `a${THUMBS}b`;
  // Insertion point inside the pair snaps left, before the pair.
  assert.equal(applyEdit(text, { start: 2, end: 2, text: 'X' }), `aX${THUMBS}b`);
  // Deleting "half" of the pair deletes the whole pair.
  assert.equal(applyEdit(text, { start: 1, end: 2, text: '' }), 'ab');
  assert.equal(applyEdit(text, { start: 2, end: 3, text: '' }), 'ab');
  for (const result of [
    applyEdit(text, { start: 2, end: 2, text: 'X' }),
    applyEdit(text, { start: 1, end: 2, text: '' }),
  ]) {
    assert.equal(hasLoneSurrogates(result), false);
  }
});

test('hasLoneSurrogates detects broken strings only', () => {
  assert.equal(hasLoneSurrogates(`a${THUMBS}b`), false);
  assert.equal(hasLoneSurrogates('a\ud83d'), true);
  assert.equal(hasLoneSurrogates('\udc4db'), true);
  assert.equal(hasLoneSurrogates(''), false);
});

test('CRLF counts as two UTF-16 offsets', () => {
  const crlf = 'ab\r\ncd';
  const lf = 'ab\ncd';
  assert.equal(crlf.length, 6);
  assert.equal(lf.length, 5);
  // Same logical edit (insert before 'c') needs offset 4 for CRLF, 3 for LF.
  assert.equal(applyEdit(crlf, { start: 4, end: 4, text: 'X' }), 'ab\r\nXcd');
  assert.equal(applyEdit(lf, { start: 3, end: 3, text: 'X' }), 'ab\nXcd');
  // Deleting the line break: 2 units for CRLF, 1 for LF.
  assert.equal(applyEdit(crlf, { start: 2, end: 4, text: '' }), 'abcd');
  assert.equal(applyEdit(lf, { start: 2, end: 3, text: '' }), 'abcd');
});

test('conflict rules are deterministic and public', () => {
  const comp = { start: 2, end: 5 };
  // Pure insertions: strictly inside conflicts, boundaries do not.
  assert.equal(editsConflict(comp, { start: 3, end: 3, text: 'x' }), true);
  assert.equal(editsConflict(comp, { start: 2, end: 2, text: 'x' }), false);
  assert.equal(editsConflict(comp, { start: 5, end: 5, text: 'x' }), false);
  assert.equal(editsConflict(comp, { start: 0, end: 0, text: 'x' }), false);
  // Deletions/replacements: any overlap conflicts.
  assert.equal(editsConflict(comp, { start: 4, end: 6, text: '' }), true);
  assert.equal(editsConflict(comp, { start: 0, end: 2, text: '' }), false);
  assert.equal(editsConflict(comp, { start: 5, end: 7, text: '' }), false);
  assert.equal(editsConflict(comp, { start: 0, end: 9, text: '' }), true);
});

test('non-conflicting edits transform the composition range', () => {
  const comp = { start: 2, end: 5 };
  // Before: shift right by inserted length.
  assert.deepEqual(transformRange(comp, { start: 0, end: 0, text: 'XY' }), {
    start: 4,
    end: 7,
  });
  // Before: deletion shifts left.
  assert.deepEqual(transformRange(comp, { start: 0, end: 1, text: '' }), {
    start: 1,
    end: 4,
  });
  // Insertion exactly at the start goes BEFORE the composition.
  assert.deepEqual(transformRange(comp, { start: 2, end: 2, text: 'XY' }), {
    start: 4,
    end: 7,
  });
  // Insertion exactly at the end goes AFTER the composition.
  assert.deepEqual(transformRange(comp, { start: 5, end: 5, text: 'XY' }), {
    start: 2,
    end: 5,
  });
  // After: unchanged.
  assert.deepEqual(transformRange(comp, { start: 7, end: 8, text: 'XYZ' }), {
    start: 2,
    end: 5,
  });
});
