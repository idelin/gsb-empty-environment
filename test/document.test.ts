import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TextDocument } from '../src/index.ts';

test('LF: positionAt/offsetAt round-trip', () => {
  const doc = new TextDocument('ab\ncde\nf');
  assert.equal(doc.lineCount, 3);
  assert.deepEqual(doc.positionAt(0), { line: 0, character: 0 });
  assert.deepEqual(doc.positionAt(3), { line: 1, character: 0 });
  assert.deepEqual(doc.positionAt(5), { line: 1, character: 2 });
  assert.deepEqual(doc.positionAt(7), { line: 2, character: 0 });
  assert.deepEqual(doc.positionAt(8), { line: 2, character: 1 }); // EOF
  assert.equal(doc.offsetAt({ line: 1, character: 2 }), 5);
  assert.equal(doc.offsetAt({ line: 2, character: 0 }), 7);
});

test('CRLF: line content excludes the terminator', () => {
  const doc = new TextDocument('ab\r\ncde\r\nf');
  assert.equal(doc.lineCount, 3);
  // 'a'=0 'b'=1 '\r'=2 '\n'=3 'c'=4 ...
  assert.deepEqual(doc.positionAt(4), { line: 1, character: 0 });
  assert.deepEqual(doc.positionAt(6), { line: 1, character: 2 });
  // character past the line content clamps to before "\r\n"
  assert.equal(doc.offsetAt({ line: 0, character: 99 }), 2);
  assert.equal(doc.offsetAt({ line: 1, character: 3 }), 7);
  assert.equal(doc.offsetAt({ line: 2, character: 0 }), 9);
});

test('CRLF: edits keep the line index correct', () => {
  const doc = new TextDocument('one\r\ntwo\r\nthree');
  doc.applyEdit(6, 6, 'x\r\ny'); // insert after "t" on line 1 -> "tx\r\nywo"
  assert.equal(doc.text, 'one\r\ntx\r\nywo\r\nthree');
  assert.equal(doc.lineCount, 4);
  assert.deepEqual(doc.positionAt(doc.offsetAt({ line: 3, character: 2 })), { line: 3, character: 2 });
  assert.equal(doc.offsetAt({ line: 3, character: 0 }), 'one\r\ntx\r\nywo\r\n'.length);
});

test('mixed LF and CRLF in one document', () => {
  const doc = new TextDocument('a\nb\r\nc');
  assert.equal(doc.lineCount, 3);
  assert.equal(doc.offsetAt({ line: 1, character: 0 }), 2);
  assert.equal(doc.offsetAt({ line: 2, character: 0 }), 5);
  assert.equal(doc.offsetAt({ line: 1, character: 10 }), 3); // clamps before \r\n
});

test('UTF-16: emoji counts as two code units', () => {
  const doc = new TextDocument('a😀b');
  assert.equal(doc.text.length, 4);
  assert.deepEqual(doc.positionAt(3), { line: 0, character: 3 }); // 'b'
  assert.equal(doc.offsetAt({ line: 0, character: 3 }), 3);
});

test('UTF-16: positionAt never lands inside a surrogate pair', () => {
  const doc = new TextDocument('a😀b');
  // offset 2 is between the high and low surrogate of 😀
  assert.deepEqual(doc.positionAt(2), { line: 0, character: 1 });
  assert.equal(doc.offsetAt({ line: 0, character: 2 }), 1);
});

test('UTF-16: edits may not split a surrogate pair', () => {
  const doc = new TextDocument('x😀y');
  assert.throws(() => doc.applyEdit(2, 2, '!'), RangeError); // start inside pair
  assert.throws(() => doc.applyEdit(0, 2, '!'), RangeError); // end inside pair
  assert.throws(() => doc.applyEdit(0, 0, '\uDE00'), RangeError); // lone low surrogate
  assert.throws(() => doc.applyEdit(0, 0, '\uD83D'), RangeError); // lone high surrogate
  // replacing the whole pair is fine
  doc.applyEdit(1, 3, '🎉');
  assert.equal(doc.text, 'x🎉y');
});

test('invalid edit ranges are rejected', () => {
  const doc = new TextDocument('abc');
  assert.throws(() => doc.applyEdit(-1, 1, ''), RangeError);
  assert.throws(() => doc.applyEdit(2, 1, ''), RangeError);
  assert.throws(() => doc.applyEdit(0, 4, ''), RangeError);
});
