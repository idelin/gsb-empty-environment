import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BTree } from '../src/btree.ts';
import { Pager, PAGE_SIZE } from '../src/pager.ts';
import { OverflowChain, OVERFLOW_CHUNK_CAPACITY, OVERFLOW_PAGE_TAG } from '../src/overflow.ts';

const KB = 1024;

function makeText(bytes: number): string {
  const unit = 'The quick brown fox jumps over the lazy dog. 0123456789 abcdef.\n';
  return unit.repeat(Math.ceil(bytes / unit.length)).slice(0, bytes);
}

test('small key/value stays inline, no overflow pages allocated', () => {
  const tree = new BTree();
  tree.insert('hello', 'world');
  const info = tree.inspectEntry('hello');
  assert.ok(info);
  assert.equal(info.keyInline, true);
  assert.equal(info.valueInline, true);
  assert.deepEqual(info.valueOverflowChain, []);
  assert.equal(tree.stats().pageCount, 1); // only the root leaf page
  assert.equal(tree.get('hello')!.toString(), 'world');
});

test('payload exactly at threshold stays inline, threshold+1 overflows', () => {
  const maxInline = Math.floor(PAGE_SIZE / 4);
  const tree = new BTree();
  tree.insert('at', Buffer.alloc(maxInline, 0x61));
  tree.insert('over', Buffer.alloc(maxInline + 1, 0x62));
  assert.equal(tree.inspectEntry('at')!.valueInline, true);
  assert.equal(tree.inspectEntry('over')!.valueInline, false);
  assert.equal(tree.get('at')!.length, maxInline);
  assert.equal(tree.get('over')!.length, maxInline + 1);
});

test('64KB long text: leaf keeps prefix + overflow chain, read reassembles transparently', () => {
  const tree = new BTree();
  const bigText = makeText(64 * KB);
  assert.equal(Buffer.byteLength(bigText), 64 * KB);

  tree.insert('user:1:bio', bigText);

  const info = tree.inspectEntry('user:1:bio')!;
  // leaf stores only a 128-byte prefix plus the chain head pointer
  assert.equal(info.valueInline, false);
  assert.equal(info.valuePrefix.length, 128);
  assert.equal(info.valuePrefix.toString(), bigText.slice(0, 128));
  assert.equal(info.valueTotalLen, 64 * KB);
  // chain covers the full 64KB payload
  const expectedPages = Math.ceil((64 * KB) / OVERFLOW_CHUNK_CAPACITY);
  assert.equal(info.valueOverflowChain.length, expectedPages);

  // transparent reassembly
  const restored = tree.get('user:1:bio')!;
  assert.equal(restored.length, 64 * KB);
  assert.equal(restored.toString(), bigText);
});

test('overflow chain pages are well-formed and byte-exact', () => {
  const pager = new Pager();
  const chain = new OverflowChain(pager);
  const data = Buffer.from(makeText(64 * KB), 'utf8');
  const head = chain.write(data);

  const ids = chain.chainIds(head);
  assert.equal(ids.length, Math.ceil(data.length / OVERFLOW_CHUNK_CAPACITY));
  for (let i = 0; i < ids.length; i++) {
    const page = pager.read(ids[i]);
    assert.equal(page.readUInt8(0), OVERFLOW_PAGE_TAG);
    const isLast = i === ids.length - 1;
    assert.equal(page.readUInt32LE(1), isLast ? 0 : ids[i + 1]);
    const expectedLen = isLast
      ? data.length - OVERFLOW_CHUNK_CAPACITY * (ids.length - 1)
      : OVERFLOW_CHUNK_CAPACITY;
    assert.equal(page.readUInt32LE(5), expectedLen);
  }
  assert.ok(chain.read(head).equals(data));
  assert.ok(chain.read(head, data.length).equals(data));

  chain.free(head);
  assert.equal(pager.pageCount, 0);
});

test('oversized keys also go through overflow pages and stay ordered', () => {
  const tree = new BTree();
  const bigKeyA = 'key-a-' + 'x'.repeat(5 * KB);
  const bigKeyB = 'key-b-' + 'y'.repeat(5 * KB);
  tree.insert(bigKeyB, 'value-B');
  tree.insert('small-key', 'value-small');
  tree.insert(bigKeyA, 'value-A');

  const info = tree.inspectEntry(bigKeyA)!;
  assert.equal(info.keyInline, false);
  assert.equal(info.keyPrefix.length, 128);
  assert.equal(info.keyTotalLen, Buffer.byteLength(bigKeyA));
  assert.ok(info.keyOverflowChain.length >= 1);

  assert.equal(tree.get(bigKeyA)!.toString(), 'value-A');
  assert.equal(tree.get(bigKeyB)!.toString(), 'value-B');
  assert.equal(tree.get('small-key')!.toString(), 'value-small');
  assert.equal(tree.get('missing'), null);
});

test('updating an overflowed value frees its old chain', () => {
  const tree = new BTree();
  tree.insert('doc', makeText(64 * KB));
  const before = tree.stats().pageCount;
  const oldChain = tree.inspectEntry('doc')!.valueOverflowChain;
  assert.ok(oldChain.length > 0);

  tree.insert('doc', 'tiny now');
  const info = tree.inspectEntry('doc')!;
  assert.equal(info.valueInline, true);
  assert.ok(tree.stats().pageCount < before);
  assert.equal(tree.get('doc')!.toString(), 'tiny now');

  // freed pages are reused by the next overflow write
  tree.insert('doc2', makeText(64 * KB));
  assert.equal(tree.get('doc2')!.length, 64 * KB);
});
