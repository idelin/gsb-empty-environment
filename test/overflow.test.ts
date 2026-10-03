import assert from 'node:assert/strict';
import test from 'node:test';
import { BPlusTree } from '../src/btree.ts';
import {
  MAX_INLINE_FIELD_BYTES,
  OVERFLOW_PREFIX_BYTES,
  OVERFLOW_PAYLOAD_CAPACITY,
  PAGE_SIZE,
} from '../src/index.ts';
import { OverflowChain } from '../src/overflow.ts';
import { Pager } from '../src/pager.ts';

const KB = 1024;

function bigText(bytes: number): Buffer {
  const seed =
    'The quick brown fox jumps over the lazy dog. 溢出页链式挂接器。0123456789abcdef\n';
  return Buffer.from(seed.repeat(Math.ceil(bytes / Buffer.byteLength(seed)))).subarray(0, bytes);
}

test('overflow chain stores a 64KB payload across linked pages', () => {
  const pager = new Pager();
  const chain = new OverflowChain(pager);
  const payload = bigText(64 * KB);
  const { firstPageId, pageIds } = chain.write(payload);

  const expectedPages = Math.ceil(payload.length / OVERFLOW_PAYLOAD_CAPACITY);
  assert.equal(pageIds.length, expectedPages);
  assert.equal(pageIds[0], firstPageId);
  assert.deepEqual(chain.pageIdsOf(firstPageId), pageIds);

  // every page except the last links to its successor; the last terminates
  for (let i = 0; i < pageIds.length; i++) {
    const next = pager.read(pageIds[i]).readUInt32LE(1);
    assert.equal(next, i + 1 < pageIds.length ? pageIds[i + 1] : 0xffffffff);
  }
  assert.deepEqual(chain.read(firstPageId, payload.length), payload);
});

test('small values stay inline in the leaf page', () => {
  const tree = new BPlusTree();
  tree.insert(Buffer.from('alpha'), Buffer.from('small value'));
  const desc = tree.describeEntry(Buffer.from('alpha'));
  assert.equal(desc.found, true);
  assert.equal(desc.overflow, false);
  assert.equal(desc.firstOverflowPageId, null);
  assert.equal(tree.get(Buffer.from('alpha'))!.toString(), 'small value');
  assert.equal(tree.stats().overflowPageCount, 0);
});

test('64KB record: leaf keeps prefix + overflow chain, get() reassembles transparently', () => {
  const tree = new BPlusTree();
  const key = Buffer.from('row:0001');
  const value = bigText(64 * KB);
  tree.insert(key, value);

  const desc = tree.describeEntry(key);
  assert.equal(desc.found, true);
  assert.equal(desc.overflow, true);
  assert.equal(desc.totalLength, 64 * KB);
  // leaf retains only the prefix plus the chain head pointer
  assert.ok(desc.prefix);
  assert.equal(desc.prefix!.length, OVERFLOW_PREFIX_BYTES);
  assert.deepEqual(desc.prefix, value.subarray(0, OVERFLOW_PREFIX_BYTES));
  assert.notEqual(desc.firstOverflowPageId, null);
  // remainder (64KB - 64B prefix) is chained across many overflow pages
  const expectedPages = Math.ceil((64 * KB - OVERFLOW_PREFIX_BYTES) / OVERFLOW_PAYLOAD_CAPACITY);
  assert.equal(desc.overflowChainPageIds.length, expectedPages);
  // transparent reassembly
  const restored = tree.get(key);
  assert.ok(restored);
  assert.equal(restored.length, value.length);
  assert.deepEqual(restored, value);
});

test('oversized keys also spill to overflow pages', () => {
  const tree = new BPlusTree();
  const key = bigText(3 * KB);
  const value = Buffer.from('value-for-big-key');
  tree.insert(key, value);
  assert.ok(key.length > MAX_INLINE_FIELD_BYTES);
  assert.deepEqual(tree.get(key), value);
  assert.equal(tree.get(bigText(3 * KB - 1)), null);
});

test('many records with mixed sizes survive leaf/internal splits', () => {
  const tree = new BPlusTree();
  const records = new Map<string, Buffer>();
  for (let i = 0; i < 300; i++) {
    const key = `key:${String(i).padStart(5, '0')}`;
    const value = i % 25 === 0 ? bigText(8 * KB + i) : Buffer.from(`value-${i}`);
    records.set(key, value);
    tree.insert(Buffer.from(key), value);
  }
  assert.ok(tree.stats().pageCount > 3, 'tree should have split into multiple pages');
  for (const [key, value] of records) {
    assert.deepEqual(tree.get(Buffer.from(key)), value, `mismatch for ${key}`);
  }
  assert.equal(tree.get(Buffer.from('key:99999')), null);
});

test('re-inserting the same key replaces the value', () => {
  const tree = new BPlusTree();
  const key = Buffer.from('row:0001');
  tree.insert(key, bigText(64 * KB));
  tree.insert(key, Buffer.from('short'));
  assert.equal(tree.get(key)!.toString(), 'short');
  tree.insert(key, bigText(32 * KB));
  assert.equal(tree.get(key)!.length, 32 * KB);
});

test('page size sanity: inline threshold and prefix fit within one page', () => {
  assert.ok(MAX_INLINE_FIELD_BYTES < PAGE_SIZE);
  assert.ok(OVERFLOW_PREFIX_BYTES < MAX_INLINE_FIELD_BYTES);
});
