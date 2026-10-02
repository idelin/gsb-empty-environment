import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MemTable, ENTRY_OVERHEAD_BYTES } from '../src/memtable.ts';

test('size accounting and threshold detection', () => {
  const mt = new MemTable(0, 1024);
  assert.equal(mt.isFull, false);
  mt.apply('k', Buffer.alloc(1010));
  assert.equal(mt.sizeBytes, 1 + 1010 + ENTRY_OVERHEAD_BYTES);
  assert.equal(mt.isFull, true);
});

test('overwrite does not double-count size', () => {
  const mt = new MemTable(0, 1 << 20);
  mt.apply('key', Buffer.alloc(100));
  const afterFirst = mt.sizeBytes;
  mt.apply('key', Buffer.alloc(200));
  assert.equal(mt.sizeBytes, afterFirst - 100 + 200);
  assert.equal(mt.entryCount, 1);
});

test('tombstone stored and sorted entries emitted', () => {
  const mt = new MemTable(0, 1 << 20);
  mt.apply('b', Buffer.from('2'));
  mt.apply('a', Buffer.from('1'));
  mt.apply('b', null);
  assert.equal(mt.get('b'), null);
  assert.deepEqual(
    mt.sortedEntries().map(([k, v]) => [k, v === null ? null : v.toString()]),
    [['a', '1'], ['b', null]],
  );
});
