import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promises as fs } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import {
  GridIndexStore,
  FaultInjector,
  LossyChannel,
  ReplicaSender,
  ReplicaReceiver,
  runUntilConverged,
  enospcError,
  latLngToCell,
} from '../src/index.ts';
import type { ReplicatedOp } from '../src/index.ts';

async function tempDir(): Promise<string> {
  return fs.mkdtemp(path.join(os.tmpdir(), 'hexfault-'));
}

test('power loss before commit: operation absent after reboot', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  await store.put('survivor', '1');
  io.powerLossDuringAppend(0.7); // torn write, then machine dies
  await assert.rejects(store.put('victim', '2'));
  io.reset(); // reboot
  const recovered = await GridIndexStore.open(dir, io);
  assert.equal(recovered.get('survivor'), '1');
  assert.ok(!recovered.has('victim'));
  await recovered.close();
});

test('power loss after successful commit: data survives', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  await store.put('a', '1');
  // commit's WAL append lands on disk, machine dies before fsync
  io.powerLossAfterAppend();
  try {
    await store.put('b', '2');
  } catch {
    // durability could not be confirmed; the record is on disk anyway
  }
  io.reset();
  const recovered = await GridIndexStore.open(dir, io);
  assert.equal(recovered.get('a'), '1');
  assert.equal(recovered.get('b'), '2');
  await recovered.close();
});

test('power loss during checkpoint: old snapshot and WAL stay valid', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  for (let i = 0; i < 10; i++) await store.put(`k${i}`, `v${i}`);
  io.failNextWrite(enospcError()); // checkpoint snapshot write fails
  await assert.rejects(store.checkpoint(), /previous state intact/);
  // state in memory is untouched and usable
  assert.equal(store.size, 10);
  await store.put('post', 'x');
  await store.close();
  const recovered = await GridIndexStore.open(dir);
  assert.equal(recovered.size, 11);
  await recovered.close();
});

test('disk full during commit: rollback leaves zero partial records', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  const store = await GridIndexStore.open(dir, io);
  await store.put('base', '0');
  io.failNextAppend(enospcError());
  await assert.rejects(
    store.commit([
      { type: 'put', cell: 'x1', payload: '1' },
      { type: 'put', cell: 'x2', payload: '2' },
      { type: 'put', cell: 'x3', payload: '3' },
    ]),
  );
  assert.equal(store.size, 1);
  await store.close();
  // every WAL line is a complete, checksum-valid record
  const wal = await fs.readFile(path.join(dir, 'wal.log'), 'utf8');
  const lines = wal.trim().split('\n').filter(Boolean);
  assert.equal(lines.length, 1, 'only the committed record remains');
  const recovered = await GridIndexStore.open(dir);
  assert.equal(recovered.size, 1);
  await recovered.close();
});

test('repeated power losses converge to a consistent state', async () => {
  const dir = await tempDir();
  const io = new FaultInjector();
  let store = await GridIndexStore.open(dir, io);
  let expected = 0;
  for (let round = 0; round < 8; round++) {
    try {
      if (round % 2 === 1) io.powerLossDuringAppend(0.3);
      await store.put(`round-${round}`, 'v');
      if (round % 2 === 1) io.reset();
      expected++;
    } catch {
      io.reset();
    }
    if (round % 3 === 0) {
      try {
        await store.checkpoint();
      } catch {
        io.reset();
      }
    }
  }
  // reboot cleanly and verify: state is exactly the set of ops whose
  // commit reported success
  const recovered = await GridIndexStore.open(dir, io);
  assert.equal(recovered.size, expected);
  for (let i = 0; i < 8; i++) {
    const wasCommitted = i % 2 === 0; // odd rounds crashed mid-append
    assert.equal(recovered.has(`round-${i}`), wasCommitted, `round ${i}`);
  }
  await recovered.close();
});

test('packet loss, duplication and reordering: replication converges', async () => {
  const opsCh = new LossyChannel({ dropRate: 0.35, duplicateRate: 0.15, seed: 7 });
  const ackCh = new LossyChannel({ dropRate: 0.35, duplicateRate: 0.15, seed: 13 });
  const sender = new ReplicaSender(opsCh, ackCh);
  const received = new Map<string, string>();
  const receiver = new ReplicaReceiver(
    opsCh,
    (op: ReplicatedOp) => {
      if (op.type === 'put') received.set(op.cell, op.payload ?? '');
      else received.delete(op.cell);
    },
    ackCh,
  );
  const total = 120;
  for (let i = 0; i < total; i++) {
    sender.send({
      opId: `op-${i}`,
      type: 'put',
      cell: latLngToCell(10 + i * 0.1, 20 + i * 0.1, 8),
      payload: `payload-${i}`,
    });
  }
  sender.send({ opId: 'op-del', type: 'del', cell: 'nonexistent', payload: null });
  const rounds = runUntilConverged(sender, [opsCh, ackCh]);
  assert.ok(rounds < 10_000, 'converged in bounded rounds');
  assert.equal(receiver.received, total + 1, 'all ops delivered exactly once logically');
  assert.equal(received.size, total);
  assert.ok(opsCh.dropped > 0, 'loss actually injected');
  assert.ok(opsCh.duplicated > 0, 'duplication actually injected');
});

test('replication into a store is idempotent under duplicate delivery', async () => {
  const dir = await tempDir();
  const store = await GridIndexStore.open(dir);
  const op = { opId: 'dup-1', type: 'put' as const, cell: 'c1', payload: 'p1' };
  assert.ok(store.applyReplicated(op));
  assert.ok(!store.applyReplicated(op), 'duplicate opId ignored');
  assert.ok(!store.applyReplicated(op));
  assert.equal(store.size, 1);
  await store.close();
});
