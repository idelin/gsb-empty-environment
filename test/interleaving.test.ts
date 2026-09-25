import { test } from 'node:test';
import assert from 'node:assert/strict';
import { tempDir, openStore, solidTile } from './helpers.ts';
import { TileStore } from '../src/store.ts';

type Op = 'W' | 'P' | 'R' | 'G' | 'G2';

function permutations<T>(items: T[]): T[][] {
  if (items.length <= 1) return [items];
  const out: T[][] = [];
  for (let i = 0; i < items.length; i++) {
    const rest = [...items.slice(0, i), ...items.slice(i + 1)];
    for (const perm of permutations(rest)) out.push([items[i], ...perm]);
  }
  return out;
}

function runSequence(ops: Op[]): void {
  const dir = tempDir();
  let store = openStore(dir);
  const baseId = store.putTile(solidTile(4, 5, 5, 5));
  store.commit((tx) => tx.publishRoot('base', 'layer', [baseId]));
  store.acquireLease('lk', 'base');

  let writtenId: string | null = null;
  let published = false;
  for (const op of ops) {
    switch (op) {
      case 'W':
        writtenId = store.putTile(solidTile(4, 200, 0, 0));
        break;
      case 'P':
        try {
          store.commit((tx) => tx.publishRoot('r2', 'layer', [writtenId!]));
          published = true;
        } catch (err) {
          assert.ok(
            err instanceof Error && err.message.includes('unknown block'),
            `unexpected publish failure: ${err}`,
          );
          assert.equal(store.getRoot('r2'), undefined, 'rejected commit leaves no root behind');
        }
        break;
      case 'R':
        store.releaseLease('lk');
        break;
      case 'G':
      case 'G2':
        store.gc();
        break;
    }
    store = openStore(dir);
    assert.deepEqual(
      store.checkInvariant(),
      [],
      `invariant broken after ${op} in sequence ${ops.join('')}`,
    );
  }
  assert.ok(store.hasBlock(baseId), `base root block must survive sequence ${ops.join('')}`);
  if (published) {
    assert.ok(
      store.hasBlock(writtenId!),
      `published root block must survive sequence ${ops.join('')}`,
    );
    assert.equal(store.getRoot('r2')!.tiles[0], writtenId);
  }
}

test('interleavings of block write, root publish, lease release and reclamation', () => {
  const ops: Op[] = ['W', 'P', 'R', 'G', 'G2'];
  const all = permutations(ops).filter(
    (seq) => seq.indexOf('W') < seq.indexOf('P'),
  );
  assert.equal(all.length, 60);
  for (const seq of all) {
    runSequence(seq);
  }
});

test('publishing a root referencing an unknown block is rejected atomically', () => {
  const dir = tempDir();
  const store = openStore(dir);
  const rootsBefore = store.listRoots().length;
  assert.throws(() => {
    store.commit((tx) => tx.publishRoot('bad', 'layer', ['no-such-block']));
  });
  assert.equal(store.listRoots().length, rootsBefore);
  assert.deepEqual(store.checkInvariant(), []);
});

test('lease released before GC never exposes a leased-then-deleted snapshot', () => {
  const dir = tempDir();
  let store = openStore(dir);
  const a = store.putTile(solidTile(4, 1, 2, 3));
  const b = store.putTile(solidTile(4, 4, 5, 6));
  store.commit((tx) => tx.publishRoot('snap', 'snapshot', [a, b]));
  store.acquireLease('reader', 'snap');
  store.commit((tx) => tx.removeRoot('snap'));
  store.gc();
  store.gc();
  store.gc();
  assert.ok(store.hasBlock(a) && store.hasBlock(b), 'lease still pins blocks');
  store.releaseLease('reader');
  store.gc();
  store.gc();
  store.gc();
  assert.ok(!store.hasBlock(a) && !store.hasBlock(b), 'reclaimed after release');
  store = openStore(dir);
  assert.deepEqual(store.checkInvariant(), []);
});
