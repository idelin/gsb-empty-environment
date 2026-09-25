import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as path from 'node:path';
import { tempDir, makeDoc, solidTile, openStore } from './helpers.ts';
import { getPixel } from '../src/image.ts';
import { Document } from '../src/document.ts';

test('generational GC: young unreferenced blocks survive their first cycles', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.editTile('A', 0, 0, solidTile(8, 1, 2, 3));
  const staleId = store.putTile(solidTile(8, 9, 9, 9));

  let report = store.gc();
  assert.ok(!report.reclaimed.includes(staleId), 'first cycle must not reclaim young block');
  report = store.gc();
  assert.ok(report.reclaimed.includes(staleId), 'old, unreferenced, unreachable block reclaimed');
  assert.equal(store.hasBlock(staleId), false);
});

test('GC re-verifies with live-root traversal, not just the refcount ledger', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.editTile('A', 0, 0, solidTile(8, 77, 0, 0));
  const liveId = store.getRoot('A')!.tiles[0];
  const blankId = store.getRoot('A')!.tiles[1];
  store.gc();
  store.gc();

  const manifestPath = path.join(dir, 'manifest.json');
  const corrupted = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
  corrupted.ledger = {};
  fs.writeFileSync(manifestPath, JSON.stringify(corrupted));

  const reopened = openStore(dir);
  assert.equal(reopened.ledgerRef(liveId), 0, 'ledger corruption in place');
  reopened.gc();
  reopened.gc();

  assert.equal(reopened.hasBlock(liveId), true, 'traversal keeps live block despite zeroed ledger');
  assert.equal(reopened.hasBlock(blankId), true);
  const doc2 = Document.open(reopened);
  assert.deepEqual(getPixel(doc2.renderLayer('A'), 0, 0), [77, 0, 0, 255]);
  assert.deepEqual(reopened.checkInvariant(), []);
});

test('read lease pins blocks of a snapshot even after the snapshot is deleted', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  doc.editTile('A', 0, 0, solidTile(8, 250, 1, 1));
  doc.snapshot('A', 's1');
  const redId = doc.snapshotTiles('s1')[0];

  store.acquireLease('lease-1', 's1');
  doc.deleteSnapshot('s1');
  doc.editTile('A', 0, 0, solidTile(8, 3, 3, 3));
  store.gc();
  store.gc();
  store.gc();
  assert.equal(store.hasBlock(redId), true, 'lease protects block from reclamation');

  store.releaseLease('lease-1');
  store.gc();
  store.gc();
  assert.equal(store.hasBlock(redId), false, 'reclaimed once lease released and generations pass');
  assert.deepEqual(store.checkInvariant(), []);
});

test('orphan block files not in the manifest are swept', () => {
  const dir = tempDir();
  const { store, doc } = makeDoc(dir);
  doc.createLayer('A');
  const orphanPath = path.join(dir, 'blocks', 'orphan.rgba');
  fs.writeFileSync(orphanPath, new Uint8Array(16));
  const report = store.gc();
  assert.ok(report.sweptOrphanFiles.includes('orphan.rgba'));
  assert.equal(fs.existsSync(orphanPath), false);
});
