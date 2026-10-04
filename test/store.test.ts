import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { GridIndexStore, FaultInjector, enospcError } from '../src/index.ts';

async function tempDir(): Promise<string> {
  return fs.mkdtemp(path.join(os.tmpdir(), 'hexstore-'));
}

async function files(dir: string): Promise<string[]> {
  return (await fs.readdir(dir)).sort();
}

test('put/get/delete with persistence across reopen', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  await store.put('18228614efffffff', 'beijing');
  await store.put('18228614ffffffff', 'cell-2');
  await store.delete('18228614ffffffff');
  await store.close();

  const reopened = await GridIndexStore.open(dir);
  assert.equal(reopened.get('18228614efffffff'), 'beijing');
  assert.ok(!reopened.has('18228614ffffffff'));
  assert.equal(reopened.size, 1);
  await reopened.close();
});

test('WAL replay recovers committed data without checkpoint', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  for (let i = 0; i < 50; i++) await store.put(`cell-${i}`, `v${i}`);
  await store.close();
  const reopened = await GridIndexStore.open(dir);
  assert.equal(reopened.size, 50);
  assert.equal(reopened.get('cell-49'), 'v49');
  await reopened.close();
});

test('checkpoint compacts WAL and preserves state', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  for (let i = 0; i < 20; i++) await store.put(`cell-${i}`, `v${i}`);
  await store.checkpoint();
  const walSize = (await fs.stat(path.join(dir, 'wal.log'))).size;
  assert.equal(walSize, 0, 'WAL truncated after checkpoint');
  await store.close();
  const reopened = await GridIndexStore.open(dir);
  assert.equal(reopened.size, 20);
  await reopened.close();
});

test('ENOSPC during commit rolls back: state and WAL untouched', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  await store.put('stable', 'committed-before');
  const walBefore = (await fs.stat(path.join(dir, 'wal.log'))).size;

  io.failNextAppend(enospcError());
  await assert.rejects(store.put('doomed', 'x'), /rolled back/);
  assert.ok(!store.has('doomed'), 'rolled-back op not visible');
  assert.equal(store.size, 1);
  const walAfter = (await fs.stat(path.join(dir, 'wal.log'))).size;
  assert.equal(walAfter, walBefore, 'WAL restored to pre-transaction size');

  // store keeps working after the transient failure
  await store.put('after-failure', 'ok');
  assert.equal(store.get('after-failure'), 'ok');
  await store.close();

  const reopened = await GridIndexStore.open(dir);
  assert.equal(reopened.size, 2);
  assert.ok(!reopened.has('doomed'));
  await reopened.close();
});

test('torn WAL tail from power loss is discarded on recovery', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  await store.put('committed', 'yes');
  // power loss in the middle of the next commit's WAL append
  io.powerLossDuringAppend(0.4);
  await assert.rejects(store.put('torn', 'partial'));
  // machine is dead: do NOT close the store; reboot with a fresh instance
  io.reset();
  const recovered = await GridIndexStore.open(dir, io);
  assert.equal(recovered.get('committed'), 'yes');
  assert.ok(!recovered.has('torn'), 'torn record must be discarded');
  // WAL was truncated to the valid tail
  const wal = await fs.readFile(path.join(dir, 'wal.log'), 'utf8');
  for (const line of wal.trim().split('\n')) {
    assert.doesNotThrow(() => JSON.parse(line));
  }
  // store self-heals and accepts new writes
  await recovered.put('post-recovery', 'ok');
  await recovered.close();
  const again = await GridIndexStore.open(dir);
  assert.equal(again.size, 2);
  await again.close();
});

test('corrupt snapshot is quarantined and WAL still replays', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  await store.put('a', '1');
  await store.checkpoint();
  await store.put('b', '2'); // post-checkpoint, lives only in WAL
  await store.close();
  // corrupt the snapshot on "disk"
  await fs.writeFile(path.join(dir, 'snapshot.json'), '{"seq":99,"entries":');
  const recovered = await GridIndexStore.open(dir);
  const names = await files(dir);
  assert.ok(
    names.some((n) => n.startsWith('snapshot.json.corrupt-')),
    'corrupt snapshot quarantined',
  );
  assert.ok(recovered.has('b'), 'WAL replay survives snapshot loss');
  await recovered.close();
});

test('double recovery is idempotent (replay produces identical state)', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  for (let i = 0; i < 10; i++) await store.put(`k${i}`, `v${i}`);
  await store.close();
  const first = await GridIndexStore.open(dir);
  const firstEntries = first.entries();
  await first.close();
  const second = await GridIndexStore.open(dir);
  assert.deepEqual(second.entries(), firstEntries);
  await second.close();
});

test('operations after close are rejected; close is idempotent', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  await store.put('x', '1');
  await store.close();
  await store.close();
  await assert.rejects(store.put('y', '2'), /closed/);
  await assert.rejects(store.checkpoint(), /closed/);
});

test('no temporary files leak after normal and failed operations', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  await store.put('a', '1');
  await store.checkpoint();
  io.failNextWrite(enospcError());
  await assert.rejects(store.checkpoint(), /previous state intact/);
  io.failNextAppend(enospcError());
  await assert.rejects(store.put('b', '2'));
  await store.put('c', '3');
  await store.checkpoint();
  await store.close();
  const names = await files(dir);
  assert.deepEqual(names, ['snapshot.json', 'wal.log']);
});
