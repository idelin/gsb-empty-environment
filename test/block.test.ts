import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BlockBuilder, BlockReader, RESTART_INTERVAL } from '../src/block.ts';

function buildBlock(entries: [string, string][]): Buffer {
  const b = new BlockBuilder();
  for (const [k, v] of entries) b.add(Buffer.from(k), Buffer.from(v));
  return b.finish();
}

test('block round-trips entries with prefix compression', () => {
  const entries: [string, string][] = [];
  for (let i = 0; i < 100; i++) {
    entries.push([`user:1001:attr_${String(i).padStart(3, '0')}`, `value-${i}`]);
  }
  const reader = new BlockReader(buildBlock(entries));
  const it = reader.iterator();
  const got: string[] = [];
  for (it.seekToFirst(); it.valid(); it.next()) {
    got.push(`${it.key().toString()}=${it.value().toString()}`);
  }
  assert.equal(got.length, 100);
  assert.equal(got[0], 'user:1001:attr_000=value-0');
  assert.equal(got[99], 'user:1001:attr_099=value-99');
});

test('in-block seek lands on first key >= target across restarts', () => {
  const entries: [string, string][] = [];
  for (let i = 0; i < RESTART_INTERVAL * 5; i++) {
    entries.push([`key${String(i).padStart(4, '0')}`, `v${i}`]);
  }
  const reader = new BlockReader(buildBlock(entries));

  const it = reader.iterator();
  it.seek(Buffer.from('key0017'));
  assert.ok(it.valid());
  assert.equal(it.key().toString(), 'key0017');

  it.seek(Buffer.from('key0017a')); // between keys -> next key
  assert.ok(it.valid());
  assert.equal(it.key().toString(), 'key0018');

  it.seek(Buffer.from('zzz')); // beyond all -> invalid
  assert.ok(!it.valid());
});

test('values are zero-copy views into the block buffer', () => {
  const raw = buildBlock([['alpha', '1'], ['alpine', '2'], ['alps', '3']]);
  const reader = new BlockReader(raw);
  const it = reader.iterator();
  it.seekToFirst();
  const first = it.value();
  it.next();
  const second = it.value();
  assert.equal(first.buffer, raw.buffer);
  assert.equal(second.buffer, raw.buffer);
});
