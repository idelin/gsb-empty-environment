import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tempDir, makeDoc, solidTile, openStore } from './helpers.ts';
import { imagesEqual, getPixel } from '../src/image.ts';
import { Document } from '../src/document.ts';

test('snapshot holds old blocks after later edits (copy-on-write)', () => {
  const dir = tempDir();
  const { doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.editTile('A', 0, 0, solidTile(8, 255, 0, 0));
  doc.snapshot('A', 's1');
  const before = doc.renderSnapshot('s1');

  doc.editTile('A', 0, 0, solidTile(8, 0, 255, 0));
  doc.editTile('A', 1, 1, solidTile(8, 0, 0, 255));

  assert.ok(imagesEqual(doc.renderSnapshot('s1'), before));
  assert.deepEqual(getPixel(doc.renderLayer('A'), 0, 0), [0, 255, 0, 255]);
  assert.deepEqual(getPixel(doc.renderSnapshot('s1'), 0, 0), [255, 0, 0, 255]);
});

test('deleting a snapshot reclaims only blocks no one else references', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.editTile('A', 0, 0, solidTile(8, 255, 0, 0));
  doc.snapshot('A', 's1');
  const snapTiles = doc.snapshotTiles('s1');
  const redId = snapTiles[0];
  const blankId = snapTiles[1];

  doc.editTile('A', 0, 0, solidTile(8, 0, 255, 0));
  const greenId = store.getRoot('A')!.tiles[0];

  doc.deleteSnapshot('s1');
  store.gc();
  store.gc();
  store.gc();

  assert.equal(store.hasBlock(redId), false, 'block unique to deleted snapshot reclaimed');
  assert.equal(store.hasBlock(blankId), true, 'shared blank block survives');
  assert.equal(store.hasBlock(greenId), true, 'live layer block survives');
  assert.deepEqual(store.checkInvariant(), []);
});

test('manifest persists: reopening the store keeps roots, blocks and ledger', () => {
  const dir = tempDir();
  {
    const { store, doc } = makeDoc(dir);
    doc.createLayer('A');
    doc.editTile('A', 0, 0, solidTile(8, 200, 100, 50));
    doc.snapshot('A', 's1');
    assert.equal(store.listRoots().sort().join(','), 'A,s1');
  }
  const store = openStore(dir);
  const doc = Document.open(store);
  assert.deepEqual(getPixel(doc.renderSnapshot('s1'), 0, 0), [200, 100, 50, 255]);
  assert.deepEqual(getPixel(doc.renderLayer('A'), 0, 0), [200, 100, 50, 255]);
  assert.deepEqual(store.checkInvariant(), []);
});
