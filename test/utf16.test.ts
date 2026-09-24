import { test } from 'node:test';
import assert from 'node:assert/strict';
import { TextDoc, isUtf16Boundary } from '../src/index.ts';

test('public offsets are UTF-16 code units', () => {
  const doc = new TextDoc('A');
  doc.insertAt(0, 'a\u{1F600}b'); // 'a' + emoji (2 UTF-16 units) + 'b'
  assert.equal(doc.length, 4, 'emoji counts as 2 UTF-16 code units');
  assert.equal(doc.getText(), 'a\u{1F600}b');
  doc.insertAt(3, 'X'); // between emoji and 'b'
  assert.equal(doc.getText(), 'a\u{1F600}Xb');
});

test('offsets that would split a surrogate pair are rejected', () => {
  const doc = new TextDoc('A');
  doc.insertAt(0, '\u{1F600}\u{1F601}');
  assert.equal(doc.length, 4);
  assert.throws(() => doc.insertAt(1, 'x'), RangeError);
  assert.throws(() => doc.insertAt(3, 'x'), RangeError);
  assert.throws(() => doc.deleteAt(0, 1), RangeError);
  assert.throws(() => doc.deleteAt(1, 2), RangeError);
  assert.equal(doc.getText(), '\u{1F600}\u{1F601}', 'failed ops leave text untouched');
  // Boundaries around the pairs are fine.
  doc.insertAt(2, '|');
  assert.equal(doc.getText(), '\u{1F600}|\u{1F601}');
  doc.deleteAt(0, 2);
  assert.equal(doc.getText(), '|\u{1F601}');
});

test('isUtf16Boundary helper', () => {
  const s = 'x\u{1F600}y';
  assert.equal(isUtf16Boundary(s, 0), true);
  assert.equal(isUtf16Boundary(s, 1), true);
  assert.equal(isUtf16Boundary(s, 2), false, 'inside the surrogate pair');
  assert.equal(isUtf16Boundary(s, 3), true);
  assert.equal(isUtf16Boundary(s, 4), true);
  assert.equal(isUtf16Boundary(s, 5), false, 'out of range');
  assert.equal(isUtf16Boundary(s, -1), false);
});

test('LF line endings: offsets and deletion', () => {
  const doc = new TextDoc('A');
  doc.insertAt(0, 'one\ntwo\n');
  assert.equal(doc.length, 8);
  doc.deleteAt(3, 1); // remove the LF
  assert.equal(doc.getText(), 'onetwo\n');
  doc.insertAt(7, 'three');
  assert.equal(doc.getText(), 'onetwo\nthree');
});

test('CRLF line endings: offsets count 2 units and never split the pair', () => {
  const doc = new TextDoc('A');
  doc.insertAt(0, 'one\r\ntwo');
  assert.equal(doc.length, 8, 'CRLF contributes 2 UTF-16 code units');
  doc.insertAt(5, 'X'); // right after the LF
  assert.equal(doc.getText(), 'one\r\nXtwo');
  doc.deleteAt(3, 2); // remove the whole CRLF
  assert.equal(doc.getText(), 'oneXtwo');
});

test('mixed LF and CRLF converge across replicas', () => {
  const a = new TextDoc('A');
  const ops = a.insertAt(0, 'l1\nl2\r\nl3');
  const b = new TextDoc('B');
  // Deliver shuffled but dependency-complete (chain order preserved pairwise).
  for (const op of ops) b.apply(op);
  assert.equal(b.getText(), 'l1\nl2\r\nl3');
  assert.equal(b.length, 9);
  const dels = a.deleteAt(2, 3); // removes '\n','l','2' (offsets 2..4)
  for (const op of dels) b.apply(op);
  assert.equal(a.getText(), 'l1\r\nl3');
  assert.equal(b.getText(), a.getText());
});
