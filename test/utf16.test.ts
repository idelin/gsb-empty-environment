import { test } from 'node:test';
import assert from 'node:assert/strict';
import { History } from '../src/history.ts';
import { captureContext } from '../src/diagnostic.ts';
import { snapBackward, snapForward, splitsSurrogatePair } from '../src/utf16.ts';

test('surrogate pair boundaries are detected', () => {
  const text = 'a\u{1F600}b'; // 'a' + emoji (2 UTF-16 units) + 'b'
  assert.equal(text.length, 4);
  assert.equal(splitsSurrogatePair(text, 2), true);
  assert.equal(splitsSurrogatePair(text, 1), false);
  assert.equal(splitsSurrogatePair(text, 3), false);
  assert.equal(snapBackward(text, 2), 1);
  assert.equal(snapForward(text, 2), 3);
});

test('edits may not split a surrogate pair', () => {
  const h = new History('a\u{1F600}b');
  assert.throws(
    () => h.commit([{ kind: 'insert', at: 2, text: 'x' }]),
    /splits a surrogate pair/,
  );
  assert.throws(
    () => h.commit([{ kind: 'delete', start: 1, end: 2 }]),
    /splits a surrogate pair/,
  );
  assert.throws(
    () => h.commit([{ kind: 'move', start: 0, end: 2, to: 4, moveId: 'm' }]),
    /splits a surrogate pair/,
  );
});

test('offsets are UTF-16 units: an emoji shifts the target by 2', () => {
  const h = new History('ab');
  const diag = h.bind({ range: { start: 0, end: 2 } });
  h.commit([{ kind: 'insert', at: 0, text: '\u{1F600}' }]);
  const r = h.relocate(diag);
  if (r.status !== 'certain') assert.fail(`expected certain, got ${r.status}`);
  assert.deepEqual(r.range, { start: 2, end: 4 });
});

test('context capture snaps outward and never splits surrogate pairs', () => {
  const text = '\u{1F600}\u{1F600}target\u{1F600}\u{1F600}';
  const ctx = captureContext(text, { start: 4, end: 10 }, 3);
  assert.equal(ctx.prefix, '\u{1F600}\u{1F600}');
  assert.equal(ctx.suffix, '\u{1F600}\u{1F600}');
  assert.equal(ctx.target, 'target');
});
