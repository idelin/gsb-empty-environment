import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, open } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Wal, replayWal } from '../src/wal.ts';

async function withDir(fn: (dir: string) => Promise<void>): Promise<void> {
  const dir = await mkdtemp(path.join(tmpdir(), 'wal-test-'));
  try {
    await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

test('WAL append/replay roundtrip including tombstones', async () => {
  await withDir(async (dir) => {
    const file = path.join(dir, 'wal-000000.log');
    const wal = await Wal.create(file);
    await wal.append(Buffer.from('alpha'), Buffer.from('1'));
    await wal.append(Buffer.from('beta'), Buffer.from('2'));
    await wal.append(Buffer.from('alpha'), null); // tombstone
    await wal.seal();

    const entries = await replayWal(file);
    assert.equal(entries.length, 3);
    assert.equal(entries[0].key.toString(), 'alpha');
    assert.equal(entries[0].value!.toString(), '1');
    assert.equal(entries[1].key.toString(), 'beta');
    assert.equal(entries[2].value, null);
  });
});

test('WAL rotation: sealed segment is immutable, new segment takes over', async () => {
  await withDir(async (dir) => {
    const seg0 = path.join(dir, 'wal-000000.log');
    const seg1 = path.join(dir, 'wal-000001.log');
    const wal0 = await Wal.create(seg0);
    await wal0.append(Buffer.from('k1'), Buffer.from('v1'));
    await wal0.seal();

    const wal1 = await Wal.create(seg1);
    await wal1.append(Buffer.from('k2'), Buffer.from('v2'));
    await wal1.seal();

    assert.deepEqual((await replayWal(seg0)).map((e) => e.key.toString()), ['k1']);
    assert.deepEqual((await replayWal(seg1)).map((e) => e.key.toString()), ['k2']);
  });
});

test('WAL replay stops at corrupted tail and keeps valid prefix', async () => {
  await withDir(async (dir) => {
    const file = path.join(dir, 'wal-000000.log');
    const wal = await Wal.create(file);
    await wal.append(Buffer.from('good-1'), Buffer.from('a'));
    await wal.append(Buffer.from('good-2'), Buffer.from('b'));
    await wal.seal();

    // Simulate a torn write: append garbage bytes after the sealed records.
    const fh = await open(file, 'a');
    await fh.write(Buffer.from([0xde, 0xad, 0xbe, 0xef, 0x01, 0x02, 0x03]));
    await fh.close();

    const entries = await replayWal(file);
    assert.equal(entries.length, 2);
    assert.equal(entries[1].key.toString(), 'good-2');
  });
});
