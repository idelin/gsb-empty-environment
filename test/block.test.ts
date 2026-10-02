import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BlockBuilder, BlockReader } from '../src/block.ts';

function buildBlock(keys: string[]): BlockReader {
  const builder = new BlockBuilder();
  for (const key of keys) builder.add(Buffer.from(key), Buffer.from(`v:${key}`));
  return new BlockReader(builder.finish());
}

test('block roundtrip: keys and values readable in order', () => {
  const keys = ['a', 'bb', 'ccc', 'dddd'];
  const reader = buildBlock(keys);
  assert.equal(reader.size, keys.length);
  for (let i = 0; i < keys.length; i++) {
    assert.equal(reader.keyAt(i).toString(), keys[i]);
    assert.equal(reader.valueAt(i).toString(), `v:${keys[i]}`);
  }
});

test('block binary search: exact hits, gaps, and out-of-range targets', () => {
  const reader = buildBlock(['b', 'd', 'f', 'h']);
  assert.equal(reader.seek(Buffer.from('b')), 0);
  assert.equal(reader.seek(Buffer.from('h')), 3);
  assert.equal(reader.seek(Buffer.from('a')), 0); // before first
  assert.equal(reader.seek(Buffer.from('c')), 1); // falls between -> next key
  assert.equal(reader.seek(Buffer.from('g')), 3);
  assert.equal(reader.seek(Buffer.from('z')), 4); // past last -> size
});

test('block keys/values are zero-copy views into the block buffer', () => {
  const reader = buildBlock(['alpha', 'beta']);
  const k0 = reader.keyAt(0);
  const v0 = reader.valueAt(0);
  const k1 = reader.keyAt(1);
  assert.equal(k0.buffer, reader.data.buffer);
  assert.equal(v0.buffer, reader.data.buffer);
  assert.equal(k1.buffer, reader.data.buffer);
});
