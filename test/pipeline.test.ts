import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as setImmediateP } from 'node:timers/promises';
import { LogStreamPipeline } from '../src/pipeline.ts';
import type { PersistedRecord, Sink } from '../src/sink.ts';

class MemSink implements Sink {
  records: PersistedRecord[] = [];
  syncCount = 0;

  async write(record: PersistedRecord): Promise<void> {
    this.records.push(record);
  }

  async sync(): Promise<void> {
    this.syncCount += 1;
  }

  async close(): Promise<void> {}
}

class ManualSyncSink implements Sink {
  written: PersistedRecord[] = [];
  private syncWaiters: Array<() => void> = [];

  async write(record: PersistedRecord): Promise<void> {
    this.written.push(record);
  }

  sync(): Promise<void> {
    return new Promise<void>((resolve) => {
      this.syncWaiters.push(resolve);
    });
  }

  get pendingSyncs(): number {
    return this.syncWaiters.length;
  }

  releaseOneSync(): void {
    const resolve = this.syncWaiters.shift();
    assert.ok(resolve, 'expected a pending sync to release');
    resolve();
  }

  async close(): Promise<void> {}
}

async function waitFor(cond: () => boolean, attempts = 1000): Promise<void> {
  for (let i = 0; i < attempts; i++) {
    if (cond()) return;
    await setImmediateP();
  }
  assert.ok(cond(), 'waitFor condition not met');
}

async function drainUntilWritten(sink: ManualSyncSink, n: number): Promise<void> {
  while (sink.written.length < n) {
    if (sink.pendingSyncs > 0) {
      sink.releaseOneSync();
    }
    await setImmediateP();
  }
  await waitFor(() => sink.pendingSyncs === 1);
}

test('monotonic unique LSNs across 10 concurrent producers', async () => {
  const sink = new MemSink();
  const pipeline = new LogStreamPipeline(sink);
  const lsns: number[] = [];

  async function producer(id: number): Promise<void> {
    for (let i = 0; i < 200; i++) {
      lsns.push(pipeline.append(`stream-${id}`, `payload-${id}-${i}`));
      if (i % 3 === 0) await setImmediateP();
    }
  }

  await Promise.all(Array.from({ length: 10 }, (_, id) => producer(id)));
  await pipeline.settled();

  assert.equal(lsns.length, 2000);
  assert.deepEqual([...lsns].sort((a, b) => a - b), Array.from({ length: 2000 }, (_, i) => i + 1));
  assert.equal(pipeline.published, 2000);
  assert.equal(pipeline.durable, 2000);
  assert.equal(pipeline.bufferedCount, 0);
  assert.equal(pipeline.stats.allocated, 2000);
  assert.equal(pipeline.stats.persisted, 2000);
  assert.equal(pipeline.stats.released, 2000);
  assert.equal(pipeline.stats.rolledBack, 0);
});

test('barrier blocks publishing of subsequent data until pre-barrier data is durable', async () => {
  const sink = new ManualSyncSink();
  const pipeline = new LogStreamPipeline(sink);
  const publishedEvents: number[] = [];
  pipeline.onPublish((lsn) => publishedEvents.push(lsn));

  for (let i = 0; i < 5; i++) pipeline.append('s', `pre-${i}`);
  await drainUntilWritten(sink, 5);
  assert.ok(pipeline.durable < 5, 'record 5 is written but its fsync is still held');

  let barrierResolved = false;
  const barrier = pipeline.insertBarrier().then(() => {
    barrierResolved = true;
  });

  for (let i = 0; i < 5; i++) pipeline.append('s', `post-${i}`);
  await setImmediateP();

  assert.equal(barrierResolved, false, 'barrier must wait for fsync of pre-barrier data');
  assert.ok(pipeline.published < 5, 'nothing beyond the durable frontier may be published');
  assert.equal(sink.written.length, 5, 'post-barrier records stay queued behind the held sync');

  sink.releaseOneSync();
  await barrier;
  assert.equal(barrierResolved, true);
  assert.equal(pipeline.durable, 5);
  assert.equal(pipeline.published, 5, 'publish pointer stops at the barrier frontier');

  await waitFor(() => sink.written.length === 10 && sink.pendingSyncs === 1);
  assert.equal(pipeline.published, 5, 'post-barrier data stays unpublished until its own fsync');

  sink.releaseOneSync();
  await pipeline.settled();
  assert.equal(pipeline.published, 10);
  assert.deepEqual(publishedEvents, [1, 5, 10]);
});

test('failed write rolls back unconfirmed buffers and rejects later appends', async () => {
  const failAt = 4;
  class FlakySink extends MemSink {
    override async write(record: PersistedRecord): Promise<void> {
      if (record.lsn === failAt) throw new Error(`boom at ${record.lsn}`);
      await super.write(record);
    }
  }
  const sink = new FlakySink();
  const pipeline = new LogStreamPipeline(sink);

  for (let i = 0; i < 10; i++) {
    try {
      pipeline.append('s', `rec-${i}`);
    } catch {
      break;
    }
    await setImmediateP();
  }
  await pipeline.settled();

  assert.equal(pipeline.failed, true);
  assert.throws(() => pipeline.append('s', 'late'), /boom at 4/);
  assert.equal(pipeline.bufferedCount, 0);
  assert.equal(
    pipeline.stats.allocated,
    pipeline.stats.released,
    'every allocated buffer must be released',
  );
  assert.equal(
    pipeline.stats.persisted + pipeline.stats.rolledBack,
    pipeline.stats.allocated,
    'each buffer is either persisted or rolled back',
  );
  assert.ok(pipeline.stats.rolledBack > 0);
  assert.ok(pipeline.durable < failAt);
});
