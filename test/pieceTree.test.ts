import { test } from 'node:test';
import assert from 'node:assert/strict';
import { PieceTree } from '../src/index.ts';

test('builds from initial text and indexes LF lines', () => {
  const tree = new PieceTree('one\ntwo\nthree');
  assert.equal(tree.lineCount, 3);
  assert.equal(tree.lineContent(0), 'one');
  assert.equal(tree.lineContent(1), 'two');
  assert.equal(tree.lineContent(2), 'three');
  assert.equal(tree.lineStart(1), 4);
  assert.equal(tree.lineAt(6), 1);
});

test('indexes CRLF lines and strips the terminator from content', () => {
  const tree = new PieceTree('ab\r\ncd\r\nef');
  assert.equal(tree.lineCount, 3);
  assert.equal(tree.lineContent(0), 'ab');
  assert.equal(tree.lineContent(1), 'cd');
  assert.equal(tree.lineContent(2), 'ef');
  assert.equal(tree.lineStart(1), 4); // skips both \r and \n
  assert.equal(tree.lineStart(2), 8);
});

test('insert and delete keep text and line index consistent', () => {
  const tree = new PieceTree('hello\nworld');
  tree.insert(5, '!');
  assert.equal(tree.getText(), 'hello!\nworld');
  assert.equal(tree.lineCount, 2);
  assert.equal(tree.lineContent(0), 'hello!');

  tree.insert(7, '\nnew\n');
  assert.equal(tree.getText(), 'hello!\n\nnew\nworld');
  assert.equal(tree.lineCount, 4);
  assert.equal(tree.lineContent(1), '');
  assert.equal(tree.lineContent(2), 'new');

  tree.delete(0, 8); // removes 'hello!\n\n'
  assert.equal(tree.getText(), 'new\nworld');
  assert.equal(tree.lineCount, 2);
  assert.equal(tree.lineContent(0), 'new');

  tree.delete(3, 2); // removes '\nw' -> joins lines
  assert.equal(tree.getText(), 'neworld');
  assert.equal(tree.lineCount, 1);
});

test('insert at CRLF boundary keeps both terminators intact', () => {
  const tree = new PieceTree('a\r\nb');
  tree.insert(3, 'X'); // between \n and b
  assert.equal(tree.getText(), 'a\r\nXb');
  assert.equal(tree.lineContent(0), 'a');
  assert.equal(tree.lineContent(1), 'Xb');
});

test('rejects edits that split a surrogate pair', () => {
  const tree = new PieceTree('a😀b');
  assert.throws(() => tree.insert(2, 'x'), /surrogate pair+/);
  assert.throws(() => tree.delete(1, 1), /surrogate pair/);
  assert.throws(() => tree.delete(2, 1), /surrogate pair/);
  tree.delete(1, 2); // deleting the whole pair is fine
  assert.equal(tree.getText(), 'ab');
});

test('rejects out-of-range offsets', () => {
  const tree = new PieceTree('abc');
  assert.throws(() => tree.insert(4, 'x'), RangeError);
  assert.throws(() => tree.delete(-1, 1), RangeError);
  assert.throws(() => tree.delete(2, 5), RangeError);
});

test('sequential random-ish edits match string reference', () => {
  const tree = new PieceTree('ab\ncd\r\nef');
  let ref = 'ab\ncd\r\nef';
  const ops: Array<[number, string]> = [
    [0, 'XY'],
    [5, '\t'],
    [8, '世'],
    [2, '\r\n'],
  ];
  for (const [at, text] of ops) {
    tree.insert(at, text);
    ref = ref.slice(0, at) + text + ref.slice(at);
    assert.equal(tree.getText(), ref);
  }
  const dels: Array<[number, number]> = [[0, 3], [4, 2], [1, 1]];
  for (const [at, len] of dels) {
    tree.delete(at, len);
    ref = ref.slice(0, at) + ref.slice(at + len);
    assert.equal(tree.getText(), ref);
  }
});
