import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Document, mapPosition, transformSelection } from '../src/document.ts';

test('insert: anchor affinity decides position at the insertion point', () => {
  const doc = new Document('ac');
  doc.addAnchor('left', 1, 'left');
  doc.addAnchor('right', 1, 'right');
  doc.applyEdit({ kind: 'insert', at: 1, text: 'b' });
  assert.equal(doc.text, 'abc');
  assert.equal(doc.getAnchor('left')!.offset, 1);
  assert.equal(doc.getAnchor('right')!.offset, 2);
});

test('insert: anchors before and after the insertion shift correctly', () => {
  const doc = new Document('abcdef');
  doc.addAnchor('before', 1, 'right');
  doc.addAnchor('after', 5, 'left');
  doc.applyEdit({ kind: 'insert', at: 3, text: 'XY' });
  assert.equal(doc.text, 'abcXYdef');
  assert.equal(doc.getAnchor('before')!.offset, 1);
  assert.equal(doc.getAnchor('after')!.offset, 7);
});

test('delete: covered anchor snaps to the deletion start', () => {
  const doc = new Document('abcdef');
  doc.addAnchor('inside', 4, 'right');
  doc.addAnchor('atStart', 2, 'right');
  doc.addAnchor('atEnd', 5, 'left');
  doc.applyEdit({ kind: 'delete', from: 2, to: 5 });
  assert.equal(doc.text, 'abf');
  assert.equal(doc.getAnchor('inside')!.offset, 2);
  assert.equal(doc.getAnchor('atStart')!.offset, 2);
  assert.equal(doc.getAnchor('atEnd')!.offset, 2);
});

test('replace: covered anchor snaps to start, then affinity applies to inserted text', () => {
  const doc = new Document('abcdef');
  doc.addAnchor('left', 3, 'left');
  doc.addAnchor('right', 3, 'right');
  doc.applyEdit({ kind: 'replace', from: 2, to: 5, text: 'XYZ' });
  assert.equal(doc.text, 'abXYZf');
  assert.equal(doc.getAnchor('left')!.offset, 2);
  assert.equal(doc.getAnchor('right')!.offset, 5);
});

test('mapPosition: pure insert at point honors affinity', () => {
  const edit = { kind: 'insert', at: 2, text: 'NN' } as const;
  assert.equal(mapPosition(2, edit, 'left'), 2);
  assert.equal(mapPosition(2, edit, 'right'), 4);
  assert.equal(mapPosition(1, edit, 'right'), 1);
  assert.equal(mapPosition(3, edit, 'left'), 5);
});

test('selection: both ends transform, insert at boundaries grows by default', () => {
  const doc = new Document('abcdef');
  doc.setSelection(2, 4);
  doc.applyEdit({ kind: 'insert', at: 2, text: 'X' });
  assert.deepEqual(doc.selection, { start: 2, end: 5 });
  doc.applyEdit({ kind: 'insert', at: 5, text: 'Y' });
  assert.deepEqual(doc.selection, { start: 2, end: 6 });
});

test('selection: delete covering one end snaps it to the deletion start', () => {
  const doc = new Document('abcdef');
  doc.setSelection(1, 5);
  doc.applyEdit({ kind: 'delete', from: 3, to: 6 });
  assert.equal(doc.text, 'abc');
  assert.deepEqual(doc.selection, { start: 1, end: 3 });
});

test('selection: caret-style transform keeps empty selection empty', () => {
  const sel = transformSelection(
    { start: 3, end: 3 },
    { kind: 'insert', at: 3, text: 'ab' },
    { startAffinity: 'right', endAffinity: 'right' },
  );
  assert.deepEqual(sel, { start: 5, end: 5 });
});

test('batch: rejects overlapping edits', () => {
  const doc = new Document('abcdef');
  assert.throws(
    () => doc.applyBatch([
      { kind: 'delete', from: 1, to: 4 },
      { kind: 'replace', from: 3, to: 5, text: 'x' },
    ]),
    /overlapping edits/,
  );
  assert.equal(doc.text, 'abcdef');
});

test('batch: rejects out-of-bounds edits', () => {
  const doc = new Document('abc');
  assert.throws(() => doc.applyBatch([{ kind: 'delete', from: 2, to: 4 }]), RangeError);
  assert.throws(() => doc.applyBatch([{ kind: 'insert', at: -1, text: 'x' }]), RangeError);
});

test('batch: touching ranges and same-point inserts are allowed', () => {
  const doc = new Document('abcdef');
  doc.applyBatch([
    { kind: 'delete', from: 0, to: 2 },
    { kind: 'delete', from: 2, to: 4 },
    { kind: 'insert', at: 4, text: 'X' },
    { kind: 'insert', at: 4, text: 'Y' },
  ]);
  assert.equal(doc.text, 'XYef');
});

test('batch: edits are given in original coordinates', () => {
  const doc = new Document('abcdef');
  doc.applyBatch([
    { kind: 'insert', at: 1, text: 'X' },
    { kind: 'delete', from: 4, to: 5 },
  ]);
  assert.equal(doc.text, 'aXbcdf');
});

test('undo restores text, anchors and selection; redo reapplies', () => {
  const doc = new Document('abcdef');
  doc.addAnchor('a', 4, 'right');
  doc.setSelection(1, 3);
  doc.applyEdit({ kind: 'delete', from: 2, to: 5 });
  assert.equal(doc.text, 'abf');
  assert.equal(doc.getAnchor('a')!.offset, 2);
  assert.deepEqual(doc.selection, { start: 1, end: 2 });

  assert.equal(doc.undo(), true);
  assert.equal(doc.text, 'abcdef');
  assert.equal(doc.getAnchor('a')!.offset, 4);
  assert.deepEqual(doc.selection, { start: 1, end: 3 });

  assert.equal(doc.redo(), true);
  assert.equal(doc.text, 'abf');
  assert.equal(doc.getAnchor('a')!.offset, 2);
  assert.deepEqual(doc.selection, { start: 1, end: 2 });
});

test('undo restores a whole batch as one step', () => {
  const doc = new Document('abcdef');
  doc.addAnchor('a', 0, 'left');
  doc.addAnchor('b', 6, 'right');
  doc.applyBatch([
    { kind: 'delete', from: 0, to: 1 },
    { kind: 'insert', at: 5, text: 'Z' },
  ]);
  assert.equal(doc.text, 'bcdeZf');
  assert.equal(doc.undo(), true);
  assert.equal(doc.text, 'abcdef');
  assert.deepEqual(doc.anchors().map((a) => [a.id, a.offset]), [
    ['a', 0],
    ['b', 6],
  ]);
});

test('undo on empty history returns false', () => {
  const doc = new Document('abc');
  assert.equal(doc.undo(), false);
  assert.equal(doc.redo(), false);
});
