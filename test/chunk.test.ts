import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MemChunkStore, chunkify } from '../src/chunk.ts';
import { diff } from '../src/diff.ts';
import type { Json } from '../src/json.ts';

const bigSection = (seed: string) => ({
  name: seed,
  values: Array.from({ length: 20 }, (_, i) => ({ n: i, tag: `${seed}-${i}` })),
  nested: { leaf: `${seed}-leaf` },
});

const topLevelChunks = (path: ReadonlyArray<string | number>) => path.length === 1;

test('digest pruning: only chunks on the changed path are read', () => {
  const store = new MemChunkStore();
  const base = {
    s1: bigSection('s1'),
    s2: bigSection('s2'),
    s3: bigSection('s3'),
    s4: bigSection('s4'),
    s5: bigSection('s5'),
  };
  const target = structuredClone(base) as any;
  target.s3.nested.leaf = 'changed';

  const baseChunked = chunkify(base as Json, store, topLevelChunks);
  const targetChunked = chunkify(target as Json, store, topLevelChunks);
  assert.equal(store.reads, 0); // chunkify only stores, never reads
  assert.equal(store.size, 6); // 5 sections + 1 modified copy of s3

  const d = diff(baseChunked, targetChunked, { reader: store });
  // Only the two versions of the changed chunk are materialized.
  assert.equal(store.reads, 2);
  assert.ok(store.reads < store.size);
  assert.deepEqual(d.changes, [
    { kind: 'set', path: ['s3', 'nested', 'leaf'], before: 's3-leaf', after: 'changed' },
  ]);
});

test('cross-chunk subtree move is detected without reading the chunk', () => {
  const store = new MemChunkStore();
  const subtree = { big: 'subtree', with: ['lots', 'of', 'data'] };
  const ref = store.put(subtree as Json);
  const base = { a: ref, b: 1 } as unknown as Json;
  const target = { b: 1, c: ref } as unknown as Json;

  const d = diff(base, target, { reader: store });
  assert.equal(store.reads, 0); // moved chunk never loaded
  assert.equal(d.changes.length, 1);
  assert.equal(d.changes[0].kind, 'move');
  assert.deepEqual(d.changes[0].from, ['a']);
  assert.deepEqual(d.changes[0].path, ['c']);
  assert.deepEqual(d.sourceMap.get('$.c'), { source: ['a'], kind: 'move' });
});

test('unchanged chunked trees diff to empty with zero reads', () => {
  const store = new MemChunkStore();
  const doc = { s1: bigSection('s1'), s2: bigSection('s2') };
  const chunked = chunkify(doc as Json, store, topLevelChunks);
  const d = diff(chunked, chunked, { reader: store });
  assert.equal(d.changes.length, 0);
  assert.equal(store.reads, 0);
});
