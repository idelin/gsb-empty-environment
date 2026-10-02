import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { SstableBuilder, SstableReader, shortestSeparator } from '../src/sstable.ts';
import { SstableIterator } from '../src/iterator.ts';

function tmpdir(): string {
  return fs.mkdtempSync(path.join(os.tmpdir(), 'lsm-sst-'));
}

function buildTable(keys: string[], blockSize = 128): { reader: SstableReader; dir: string } {
  const dir = tmpdir();
  const file = path.join(dir, 't.sst');
  const builder = new SstableBuilder(blockSize);
  for (const key of keys) builder.add(Buffer.from(key), Buffer.from(`v:${key}`));
  builder.finish(file);
  return { reader: new SstableReader(file), dir };
}

const KEYS: string[] = [];
for (let i = 0; i < 1000; i++) KEYS.push(`key_${String(i).padStart(6, '0')}`);

test('shortest separator shortens at the first differing byte', () => {
  assert.equal(
    shortestSeparator(Buffer.from('abc'), Buffer.from('abd')).toString(),
    'abc', // c+1 == d, cannot shorten
  );
  assert.equal(
    shortestSeparator(Buffer.from('abc'), Buffer.from('abz')).toString(),
    'abd',
  );
  assert.equal(
    shortestSeparator(Buffer.from('tenant_A:order_9'), Buffer.from('tenant_C:')).toString(),
    'tenant_B', // 'A'+1='B' < 'C', and 'tenant_B' still sorts before 'tenant_C:'
  );
  assert.equal(
    shortestSeparator(Buffer.from('tenant_A:order_9'), Buffer.from('tenant_B:')).toString(),
    'tenant_A:order_9', // 'A'+1 == 'B': cannot shorten, keep the full key
  );
});

test('iterator scans the whole table in sorted order', () => {
  const { reader } = buildTable(KEYS);
  const it = new SstableIterator(reader);
  const seen: string[] = [];
  for (it.seekToFirst(); it.valid(); it.next()) seen.push(it.key().toString());
  assert.deepEqual(seen, KEYS);
  assert.equal(reader.stats.dataBlocksRead, reader.numBlocks);
  reader.close();
});

test('seek lands exactly on the target block and reads only blocks from there on', () => {
  const { reader } = buildTable(KEYS);
  assert.ok(reader.numBlocks > 10, `expected many blocks, got ${reader.numBlocks}`);
  const it = new SstableIterator(reader);
  it.seek(Buffer.from('key_000500'));
  assert.equal(it.key().toString(), 'key_000500');
  assert.equal(reader.stats.dataBlocksRead, 1); // only the landing block so far
  // A seek between keys lands on the next key.
  it.seek(Buffer.from('key_000500!'));
  assert.equal(it.key().toString(), 'key_000501');
  // A seek past the last key invalidates the iterator without extra reads.
  const before = reader.stats.dataBlocksRead;
  it.seek(Buffer.from('zzz'));
  assert.equal(it.valid(), false);
  assert.equal(reader.stats.dataBlocksRead, before);
  reader.close();
});

test('seek only reads the blocks overlapping the scanned range', () => {
  const { reader } = buildTable(KEYS);
  const it = new SstableIterator(reader);
  it.seek(Buffer.from('key_000100'));
  let count = 0;
  while (it.valid() && it.key().toString() <= 'key_000199') {
    count++;
    it.next();
  }
  assert.equal(count, 100);
  assert.ok(
    reader.stats.dataBlocksRead <= 25,
    `expected ~20 blocks for a 100-key range, read ${reader.stats.dataBlocksRead} of ${reader.numBlocks}`,
  );
  reader.close();
});
