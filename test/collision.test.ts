import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tempDir, openStore, weakHash } from './helpers.ts';
import { bytesEqual, type Tile } from '../src/image.ts';

function tileWithBytes(bytes: number[]): Tile {
  const pixels = new Uint8Array(4 * 4 * 4);
  pixels.set(bytes);
  return { width: 4, height: 4, pixels };
}

test('digest collision bucket stores both blocks and compares full pixels', () => {
  const dir = tempDir();
  const store = openStore(dir, weakHash);

  const a = tileWithBytes([1, 0, 0, 0]);
  const b = tileWithBytes([0, 1, 0, 0]);
  assert.equal(weakHash(a.pixels), weakHash(b.pixels), 'test requires colliding digests');
  assert.ok(!bytesEqual(a.pixels, b.pixels));

  const idA = store.putTile(a);
  const idB = store.putTile(b);
  assert.notEqual(idA, idB, 'colliding but different content must get distinct ids');

  const idA2 = store.putTile(tileWithBytes([1, 0, 0, 0]));
  assert.equal(idA2, idA, 'identical content reuses the existing bucket entry');

  const gotA = store.getTile(idA);
  const gotB = store.getTile(idB);
  assert.ok(bytesEqual(gotA.pixels, a.pixels));
  assert.ok(bytesEqual(gotB.pixels, b.pixels));
  assert.equal(store.listBlocks().length, 2);
});

test('collision bucket survives manifest reload', () => {
  const dir = tempDir();
  const a = tileWithBytes([2, 0, 0, 0]);
  const b = tileWithBytes([1, 1, 0, 0]);
  let idA: string;
  let idB: string;
  {
    const store = openStore(dir, weakHash);
    idA = store.putTile(a);
    idB = store.putTile(b);
  }
  const store = openStore(dir, weakHash);
  assert.equal(store.putTile(tileWithBytes([2, 0, 0, 0])), idA);
  assert.equal(store.putTile(tileWithBytes([1, 1, 0, 0])), idB);
  assert.equal(store.listBlocks().length, 2);
  assert.ok(bytesEqual(store.getTile(idA).pixels, a.pixels));
  assert.ok(bytesEqual(store.getTile(idB).pixels, b.pixels));
});
