import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setImmediate as setImmediateP } from 'node:timers/promises';
import { LogStreamPipeline } from '../src/pipeline.ts';
import { FileSink, type PersistedRecord, type Sink } from '../src/sink.ts';

class FailAtLsnSink implements Sink {
  private readonly inner: Sink;
  private readonly failLsn: number;

  constructor(inner: Sink, failLsn: number) {
    this.inner = inner;
    this.failLsn = failLsn;
  }

  write(record: PersistedRecord): Promise<void> {
    if (record.lsn === this.failLsn) {
      return Promise.reject(new Error(`injected write failure at LSN ${record.lsn}`));
    }
    return this.inner.write(record);
  }

  sync(): Promise<void> {
    return this.inner.sync();
  }

  close(): Promise<void> {
    return this.inner.close();
  }
}

const STREAMS = 10;
const PER_STREAM = 200;
const TOTAL = STREAMS * PER_STREAM;
const BARRIER_LSNS = [500, 1200];
const FAIL_LSN = 1250;

test('acceptance: 10 streams x 2000 logs, barriers at 500/1200, failure at 1250', async (t) => {
  const dir = await mkdtemp(join(tmpdir(), 'log-pipeline-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const file = join(dir, 'wal.log');

  const pipeline = new LogStreamPipeline(new FailAtLsnSink(new FileSink(file), FAIL_LSN));

  const publishViolations: string[] = [];
  const publishEvents: number[] = [];
  pipeline.onPublish((lsn) => {
    publishEvents.push(lsn);
    if (lsn > pipeline.durable) {
      publishViolations.push(`published ${lsn} beyond durable ${pipeline.durable}`);
    }
    for (const barrierLsn of BARRIER_LSNS) {
      if (lsn > barrierLsn && pipeline.durable < barrierLsn) {
        publishViolations.push(`published ${lsn} before barrier ${barrierLsn} was durable`);
      }
    }
  });

  const sent = new Map<number, string>();
  const barriers = new Map<number, Promise<void>>();
  const barrierDurableAtResolve = new Map<number, number>();

  async function producer(streamId: string): Promise<void> {
    for (let i = 0; i < PER_STREAM; i++) {
      const payload = `${streamId}#${i}`;
      try {
        const lsn = pipeline.append(streamId, payload);
        sent.set(lsn, payload);
        if (BARRIER_LSNS.includes(lsn)) {
          const barrier = pipeline.insertBarrier().then(() => {
            barrierDurableAtResolve.set(lsn, pipeline.durable);
          });
          barriers.set(lsn, barrier);
        }
      } catch {
        return;
      }
      if (i % 5 === 0) await setImmediateP();
    }
  }

  await Promise.all(Array.from({ length: STREAMS }, (_, s) => producer(`stream-${s}`)));
  await pipeline.settled();
  await pipeline.close();

  assert.equal(pipeline.failed, true, 'pipeline must latch the injected failure');
  assert.match(String(pipeline.failureReason), /injected write failure at LSN 1250/);

  assert.deepEqual([...barriers.keys()].sort((a, b) => a - b), BARRIER_LSNS);
  await assert.doesNotReject(Promise.all(barriers.values()));
  for (const barrierLsn of BARRIER_LSNS) {
    assert.ok(
      barrierDurableAtResolve.get(barrierLsn)! >= barrierLsn,
      `barrier ${barrierLsn} resolved only after durability frontier passed it`,
    );
  }

  const lines = (await readFile(file, 'utf8')).split('\n').filter((l) => l.length > 0);
  const records = lines.map((l) => JSON.parse(l) as { lsn: number; streamId: string; payload: string });
  const persistedLsns = records.map((r) => r.lsn).sort((a, b) => a - b);

  for (let i = 0; i < persistedLsns.length; i++) {
    assert.equal(persistedLsns[i], i + 1, 'persisted records must form a gap-free prefix');
  }
  const maxPersisted = persistedLsns.length;
  assert.ok(maxPersisted >= 1200, `all logs before barrier 1200 must be durable, got ${maxPersisted}`);
  assert.ok(maxPersisted < FAIL_LSN, `nothing at or beyond failed LSN ${FAIL_LSN} may persist`);

  for (const record of records) {
    assert.equal(record.payload, sent.get(record.lsn), 'persisted payload must match what was sent');
  }

  assert.equal(pipeline.stats.persisted, maxPersisted);
  assert.equal(pipeline.stats.released, pipeline.stats.allocated, 'memory accounting must balance');
  assert.equal(
    pipeline.stats.persisted + pipeline.stats.rolledBack,
    pipeline.stats.allocated,
    'every buffer is either persisted or rolled back',
  );
  assert.ok(pipeline.stats.rolledBack > 0, 'unconfirmed buffers must be rolled back');
  assert.equal(pipeline.bufferedCount, 0, 'no buffers may leak after rollback');

  assert.equal(pipeline.published, pipeline.durable);
  assert.ok(pipeline.published >= 1200);
  assert.ok(pipeline.published < FAIL_LSN);
  assert.deepEqual(publishViolations, []);
  assert.ok(
    publishEvents.every((v, i) => i === 0 || v > publishEvents[i - 1]!),
    'publish pointer must advance monotonically',
  );

  console.log('acceptance summary:', {
    allocated: pipeline.stats.allocated,
    persisted: pipeline.stats.persisted,
    rolledBack: pipeline.stats.rolledBack,
    released: pipeline.stats.released,
    durable: pipeline.durable,
    published: pipeline.published,
  });
});
