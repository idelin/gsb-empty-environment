import assert from 'node:assert/strict';
import test from 'node:test';
import { DiskFullError, PowerLossError } from '../src/faults.ts';
import { LogShipper, LossyChannel, ReplicaReceiver } from '../src/logshipper.ts';
import {
  assertNoLeaks,
  assertPageLSNConsistent,
  boot,
  crash,
  recover,
} from './helpers.ts';

test('power loss mid-flush: torn WAL tail is detected, truncated and healed', async () => {
  const sys = boot({ seed: 43 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'a', 1);
  await sys.wal.flush(); // BEGIN + UPDATE durable
  sys.injector.arm('wal.powerLoss', 1);
  await assert.rejects(() => sys.tm.commit(t1), PowerLossError);

  // Durable media now ends in a torn record; reboot must truncate it.
  const sys2 = boot(crash(sys));
  const durable = sys2.wal.readDurable();
  assert.equal(durable.length, sys2.media.walLines.length, 'torn tail must be truncated');
  assert.ok(!durable.some((r) => r.type === 'COMMIT'), 'torn commit must not survive');

  const report = await recover(sys2);
  assert.deepEqual(report.losers, [t1], 'uncommitted txn is rolled back');
  assert.deepEqual(sys2.disk.readPage(1)!.data, {});

  // Self-healing: the system keeps working on the same media afterwards.
  const t2 = await sys2.tm.begin();
  await sys2.tm.set(t2, 2, 'b', 2);
  await sys2.tm.commit(t2);
  await sys2.wal.flush();
  await sys2.bp.flushAll();
  assert.deepEqual(sys2.disk.readPage(2)!.data, { b: 2 });
  assert.equal(
    sys2.wal.readDurable().length,
    sys2.media.walLines.length,
    'log must stay decodable after new appends',
  );
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2);
});

test('disk full on commit: commit fails safely, txn rolls back, system recovers', async () => {
  const sys = boot({ seed: 47 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'a', 1);
  await sys.tm.commit(t1);

  const t2 = await sys.tm.begin();
  await sys.tm.set(t2, 1, 'b', 2);
  sys.injector.arm('wal.flush', 1); // disk full exactly at the commit flush
  await assert.rejects(() => sys.tm.commit(t2), DiskFullError);
  assert.deepEqual(sys.tm.openTxnIds(), [t2], 'failed commit must not leak a committed txn');

  await sys.tm.abort(t2); // operator rolls the txn back after the failure
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.deepEqual(report.losers, []);
  assert.deepEqual(sys2.disk.readPage(1)!.data, { a: 1 }, 'no trace of the failed txn');
  assertNoLeaks(sys2);
});

test('disk full on page write: flush fails atomically and retries succeed', async () => {
  const sys = boot({ seed: 53 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'a', 1);
  await sys.tm.commit(t1);

  sys.injector.arm('storage.writePage', 1);
  await assert.rejects(() => sys.bp.flushPage(1), DiskFullError);
  assert.deepEqual(sys.bp.dirtyPageIds(), [1], 'page must stay dirty after failed write');
  assert.equal(sys.disk.readPage(1), undefined, 'failed write must be atomic (no partial page)');

  await sys.bp.flushPage(1); // disk freed -> retry heals
  assert.deepEqual(sys.disk.readPage(1)!.data, { a: 1 });
  assertNoLeaks(sys, { walFlushed: false });
});

test('torn page write: redo rebuilds the page from full log history', async () => {
  const sys = boot({ seed: 59 });
  const t1 = await sys.tm.begin();
  await sys.tm.set(t1, 1, 'k1', 1);
  await sys.tm.commit(t1);
  await sys.tm.checkpoint();

  const t2 = await sys.tm.begin();
  await sys.tm.set(t2, 1, 'k2', 2);
  await sys.tm.commit(t2);

  sys.injector.arm('storage.tornWrite', 1); // power cut while writing P1
  await sys.bp.flushPage(1);
  assert.throws(() => sys.disk.readPage(1), /torn/, 'page must be unreadable');

  const t3 = await sys.tm.begin();
  await sys.tm.set(t3, 1, 'k3', 3);
  await sys.wal.flush();

  const sys2 = boot(crash(sys));
  const report = await recover(sys2);
  assert.deepEqual(report.losers, [t3]);
  assert.deepEqual(
    sys2.disk.readPage(1)!.data,
    { k1: 1, k2: 2 },
    'torn page rebuilt with committed history, uncommitted k3 removed',
  );
  assertPageLSNConsistent(sys2);
  assertNoLeaks(sys2);
});

test('network packet loss (scripted): retransmission heals replication exactly', async () => {
  const sys = boot({ seed: 61 });
  for (let i = 0; i < 5; i++) {
    const t = await sys.tm.begin();
    await sys.tm.set(t, 1, `k${i}`, i);
    await sys.tm.commit(t);
  }
  const records = sys.wal.readDurable();

  sys.injector.arm('channel.send', 2); // first two data packets dropped
  sys.injector.arm('channel.ack', 1); // then one ack dropped
  const channel = new LossyChannel(sys.injector);
  const receiver = new ReplicaReceiver();
  const shipper = new LogShipper(channel, receiver);
  shipper.ship(records);

  assert.deepEqual(receiver.received, records, 'replica converges to the exact source log');
  assert.equal(channel.droppedData, 2);
  assert.equal(channel.droppedAcks, 1);
  assert.equal(receiver.duplicates, 1, 'lost ack causes exactly one deduped retransmit');
  assert.equal(shipper.retries, 3, 'one retry per lost packet/ack');
  await sys.bp.flushAll();
  assertNoLeaks(sys, { walFlushed: false });
});

test('network packet loss (probabilistic 30%/20%): replica still converges', async () => {
  const sys = boot({ seed: 67 });
  for (let i = 0; i < 8; i++) {
    const t = await sys.tm.begin();
    await sys.tm.set(t, 1, `k${i}`, i * 10);
    await sys.tm.commit(t);
  }
  const records = sys.wal.readDurable();

  sys.injector.armProbabilistic('channel.send', 0.3);
  sys.injector.armProbabilistic('channel.ack', 0.2);
  const channel = new LossyChannel(sys.injector);
  const receiver = new ReplicaReceiver();
  const shipper = new LogShipper(channel, receiver);
  shipper.ship(records);

  assert.deepEqual(
    receiver.received.map((r) => r.lsn),
    records.map((r) => r.lsn),
    'no gaps, no duplicates, in order',
  );
  assert.deepEqual(receiver.received, records);
  assert.ok(shipper.retries >= channel.droppedData + channel.droppedAcks);
  assert.ok(sys.injector.fired.length > 0, 'faults must actually have fired');
  await sys.bp.flushAll();
  assertNoLeaks(sys, { walFlushed: false });
});
