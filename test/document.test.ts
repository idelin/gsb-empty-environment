import * as assert from 'assert';
import { AnchorDocument } from '../src/index';
import { test } from './harness';

test('document: anchors are transformed through edits', () => {
  const doc = new AnchorDocument('hello world');
  doc.addAnchor('a', 0, 'left');
  doc.addAnchor('b', 5, 'right');
  doc.addAnchor('c', 8, 'left');
  doc.insert(0, '>> ');
  assert.strictEqual(doc.getAnchor('a')!.pos, 0);
  assert.strictEqual(doc.getAnchor('b')!.pos, 8);
  assert.strictEqual(doc.getAnchor('c')!.pos, 11);
  doc.delete(0, 3);
  assert.strictEqual(doc.getText(), 'hello world');
  assert.strictEqual(doc.getAnchor('b')!.pos, 5);
});

test('document: anchor covered by a deletion snaps to the deletion start', () => {
  const doc = new AnchorDocument('abcdef');
  doc.addAnchor('inside', 3, 'right');
  doc.delete(2, 5);
  assert.strictEqual(doc.getText(), 'abf');
  assert.strictEqual(doc.getAnchor('inside')!.pos, 2);
});

test('document: undo restores text and anchors', () => {
  const doc = new AnchorDocument('abcdef');
  doc.addAnchor('x', 4, 'left');
  doc.addAnchor('y', 1, 'right');
  doc.applyBatch([
    { start: 1, end: 3, text: 'Q' },
    { start: 5, end: 5, text: 'Z' },
  ]);
  assert.strictEqual(doc.getText(), 'aQdeZf');
  assert.strictEqual(doc.getAnchor('x')!.pos, 3);
  assert.strictEqual(doc.getAnchor('y')!.pos, 1);

  assert.strictEqual(doc.undo(), true);
  assert.strictEqual(doc.getText(), 'abcdef');
  assert.strictEqual(doc.getAnchor('x')!.pos, 4);
  assert.strictEqual(doc.getAnchor('y')!.pos, 1);

  // Anchor positions after a state-changing undo are the originals.
  doc.delete(0, 6);
  assert.strictEqual(doc.getText(), '');
  assert.strictEqual(doc.getAnchor('x')!.pos, 0);
  assert.strictEqual(doc.undo(), true);
  assert.strictEqual(doc.getText(), 'abcdef');
  assert.strictEqual(doc.getAnchor('x')!.pos, 4);
  assert.strictEqual(doc.getAnchor('y')!.pos, 1);

  assert.strictEqual(doc.redo(), true);
  assert.strictEqual(doc.getText(), '');
  assert.strictEqual(doc.getAnchor('x')!.pos, 0);
});

test('document: batch is a single undo step', () => {
  const doc = new AnchorDocument('abcd');
  doc.addAnchor('p', 2, 'right');
  doc.applyBatch([
    { start: 0, end: 1, text: '' },
    { start: 3, end: 3, text: '!' },
  ]);
  assert.strictEqual(doc.getText(), 'bc!d');
  doc.undo();
  assert.strictEqual(doc.getText(), 'abcd');
  assert.strictEqual(doc.getAnchor('p')!.pos, 2);
  assert.strictEqual(doc.undo(), false);
});

test('document: new edit clears the redo stack', () => {
  const doc = new AnchorDocument('ab');
  doc.insert(2, 'c');
  doc.undo();
  doc.insert(2, 'd');
  assert.strictEqual(doc.redo(), false);
  assert.strictEqual(doc.getText(), 'abd');
});

test('document: rejects out-of-bounds anchors and duplicate ids', () => {
  const doc = new AnchorDocument('ab');
  assert.throws(() => doc.addAnchor('bad', 3));
  doc.addAnchor('p', 1);
  assert.throws(() => doc.addAnchor('p', 0));
});
