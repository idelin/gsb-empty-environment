import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tempDir, makeDoc, solidTile } from './helpers.ts';
import { createImage, setPixel, getPixel, imagesEqual } from '../src/image.ts';

test('blank tiles across positions and layers share a single block', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.createLayer('B');
  assert.equal(store.listBlocks().length, 1);
  const blankId = store.listBlocks()[0];
  assert.equal(store.ledgerRef(blankId), 8);
});

test('identical tile content dedups to the same block', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  const red = solidTile(8, 255, 0, 0);
  doc.editTile('A', 0, 0, red);
  doc.editTile('A', 1, 1, solidTile(8, 255, 0, 0));
  const root = store.getRoot('A')!;
  assert.equal(root.tiles[0], root.tiles[3]);
  assert.equal(store.listBlocks().length, 2);
});

test('copy-on-write: editing one layer never affects a layer sharing its blocks', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.editTile('A', 0, 0, solidTile(8, 255, 0, 0));
  doc.cloneLayer('A', 'B');
  const sharedBefore = store.stats().blocks;
  assert.equal(store.getRoot('A')!.tiles.join(), store.getRoot('B')!.tiles.join());

  doc.editTile('B', 0, 0, solidTile(8, 0, 0, 255));

  const imgA = doc.renderLayer('A');
  const imgB = doc.renderLayer('B');
  assert.deepEqual(getPixel(imgA, 0, 0), [255, 0, 0, 255]);
  assert.deepEqual(getPixel(imgB, 0, 0), [0, 0, 255, 255]);
  assert.deepEqual(getPixel(imgA, 15, 15), [0, 0, 0, 0]);
  assert.deepEqual(getPixel(imgB, 15, 15), [0, 0, 0, 0]);
  assert.ok(store.stats().blocks > sharedBefore);
  const rootA = store.getRoot('A')!;
  const rootB = store.getRoot('B')!;
  assert.notEqual(rootA.tiles[0], rootB.tiles[0]);
  assert.equal(rootA.tiles[1], rootB.tiles[1]);
});

test('pixel-level edit round-trips through render', () => {
  const dir = tempDir();
  const { doc } = makeDoc(dir);
  doc.createLayer('A');
  const tileImg = createImage(8, 8);
  setPixel(tileImg, 3, 4, [10, 20, 30, 255]);
  doc.editTile('A', 1, 0, { width: 8, height: 8, pixels: tileImg.pixels });
  const rendered = doc.renderLayer('A');
  assert.deepEqual(getPixel(rendered, 8 + 3, 4), [10, 20, 30, 255]);
  const expected = createImage(16, 16);
  setPixel(expected, 11, 4, [10, 20, 30, 255]);
  assert.ok(imagesEqual(rendered, expected));
});
