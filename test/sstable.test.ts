import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import {
  SSTableBuilder,
  SSTableReader,
  shortestSeparator,
  shortestSuccessor,
} from '../src/sstable.ts';

function tmpFile(name: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'sstable-test-'));
  return path.join(dir, name);
}

test('shortest separator / successor', () => {
  assert.equal(shortestSeparator(Buffer.from('abc'), Buffer.from('abe')).toString(), 'abd');
  assert.equal(shortestSeparator(Buffer.from('abc'), Buffer.from('abd')).toString(), 'abc');
  // FindShortSuccessor increments the first incrementable byte
  assert.equal(shortestSuccessor(Buffer.from('abc')).toString(), 'b');
  assert.deepEqual(shortestSuccessor(Buffer.from([0xff, 0xff])), Buffer.from([0xff, 0xff]));
});

test('sstable point lookups across many blocks', () => {
  const file = tmpFile('point.sst');
  const builder = new SSTableBuilder(file, 512); // tiny blocks -> many blocks
  const N = 5000;
  for (let i = 0; i < N; i++) {
    builder.add(`user:${String(i).padStart(6, '0')}`, `payload-${i}`);
  }
  builder.finish();

  const reader = SSTableReader.open(file);
  assert.ok(reader.numBlocks > 50, `expected many blocks, got ${reader.numBlocks}`);

  for (const i of [0, 1, 123, 2500, 4999]) {
    const v = reader.get(`user:${String(i).padStart(6, '0')}`);
    assert.equal(v?.toString(), `payload-${i}`);
  }
  assert.equal(reader.get('user:999999'), null);
  assert.equal(reader.get('aaa'), null);
  // point lookups read only a handful of blocks, never the whole file
  assert.ok(reader.stats.dataBlocksRead <= 7);
});

test('locateBlock binary-searches the index block', () => {
  const file = tmpFile('locate.sst');
  const builder = new SSTableBuilder(file, 256);
  for (let i = 0; i < 2000; i++) {
    builder.add(`k${String(i).padStart(5, '0')}`, `v${i}`);
  }
  builder.finish();
  const reader = SSTableReader.open(file);

  // every key must be found in exactly the block locateBlock returns
  for (const i of [0, 777, 1999]) {
    const idx = reader.locateBlock(Buffer.from(`k${String(i).padStart(5, '0')}`));
    assert.ok(idx >= 0);
    const it = reader.readBlockAt(idx).iterator();
    it.seek(Buffer.from(`k${String(i).padStart(5, '0')}`));
    assert.ok(it.valid());
    assert.equal(it.key().toString(), `k${String(i).padStart(5, '0')}`);
  }
  // beyond the last separator -> -1
  assert.equal(reader.locateBlock(Buffer.from('zzzzzz')), -1);
});

test('builder rejects out-of-order keys', () => {
  const file = tmpFile('order.sst');
  const builder = new SSTableBuilder(file);
  builder.add('b', '1');
  assert.throws(() => builder.add('a', '2'), /increasing order/);
});
