import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MemoryGuard } from '../src/memory-guard.ts';
import { SpillQueue } from '../src/spill-queue.ts';
import { TransactionTracker } from '../src/tracker.ts';
import { transportTransaction } from '../src/transporter.ts';
import type { Chunk } from '../src/types.ts';
import { generateRows, sleep } from './helpers.ts';

function chunkCodec(): Pick<SpillQueue<Chunk>, never> & {
  serialize: (c: Chunk) => Buffer;
  deserialize: (b: Buffer) => Chunk;
} {
  return {
    serialize: (c) => Buffer.from(JSON.stringify(c), 'utf8'),
    deserialize: (b) => JSON.parse(b.toString('utf8')) as Chunk,
  };
}

test('传输器：端到端传输 10,000 行，接收端完整性校验通过', async () => {
  const tracker = new TransactionTracker('tx-e2e-small');
  const guard = new MemoryGuard(256 * 1024);
  const queue = new SpillQueue<Chunk>({ maxMemoryBytes: 64 * 1024, ...chunkCodec() });

  const stats = await transportTransaction(generateRows(10_000), {
    txId: 'tx-e2e-small',
    chunkSize: 1_000,
    guard,
    queue,
    sink: async (chunk) => {
      tracker.apply(chunk);
      await sleep(1);
    },
  });

  assert.equal(tracker.completed, true);
  assert.equal(tracker.rows, 10_000);
  assert.equal(stats.totalRows, 10_000);
  assert.equal(stats.totalChunks, 10);
  assert.ok(stats.peakInFlightBytes <= guard.limit, '在途字节峰值不得超过限流上限');
});

test('传输器：消费缓慢时触发磁盘溢写，内存驻留有界', async () => {
  const tracker = new TransactionTracker('tx-e2e-spill');
  const guard = new MemoryGuard(8 * 1024 * 1024);
  const queue = new SpillQueue<Chunk>({ maxMemoryBytes: 32 * 1024, ...chunkCodec() });

  const stats = await transportTransaction(generateRows(50_000), {
    txId: 'tx-e2e-spill',
    chunkSize: 1_000,
    guard,
    queue,
    sink: async (chunk) => {
      tracker.apply(chunk);
      await sleep(2);
    },
  });

  assert.equal(tracker.completed, true);
  assert.ok(stats.spilledBytes > 0, '消费缓慢时应有分片溢写到磁盘');
});

test('传输器：接收端能检出乱序分片', async () => {
  const tracker = new TransactionTracker('tx-order');
  const guard = new MemoryGuard(1024 * 1024);
  const queue = new SpillQueue<Chunk>({ maxMemoryBytes: 1024 * 1024, ...chunkCodec() });

  let firstData: Chunk | null = null;
  let swapped = false;
  await assert.rejects(
    transportTransaction(generateRows(100), {
      txId: 'tx-order',
      chunkSize: 50,
      guard,
      queue,
      sink: async (chunk) => {
        if (chunk.kind === 'DATA' && !swapped) {
          if (firstData === null) {
            firstData = chunk;
            return;
          }
          swapped = true;
          tracker.apply(chunk);
          tracker.apply(firstData);
          return;
        }
        tracker.apply(chunk);
      },
    }),
    /乱序或丢失/,
  );
});

test('传输器：消费端抛错时管线不死锁，错误向外传播', async () => {
  const guard = new MemoryGuard(4 * 1024); // 极小额度，生产者大概率阻塞在背压上
  const queue = new SpillQueue<Chunk>({ maxMemoryBytes: 4 * 1024, ...chunkCodec() });
  const tracker = new TransactionTracker('tx-err');

  await assert.rejects(
    transportTransaction(generateRows(100_000), {
      txId: 'tx-err',
      chunkSize: 500,
      guard,
      queue,
      sink: async (chunk) => {
        tracker.apply(chunk);
        if (chunk.kind === 'DATA' && chunk.seq === 3) {
          throw new Error('接收端模拟故障');
        }
        await sleep(1);
      },
    }),
    /接收端模拟故障/,
  );
});

test('传输器：全部溢写磁盘的极端场景下保持完整性与 FIFO（溢写竞态回归）', async () => {
  const tracker = new TransactionTracker('tx-all-spill');
  const guard = new MemoryGuard(16 * 1024 * 1024);
  const queue = new SpillQueue<Chunk>({ maxMemoryBytes: 1, ...chunkCodec() }); // 强制全量溢写

  const stats = await transportTransaction(generateRows(100_000), {
    txId: 'tx-all-spill',
    chunkSize: 500,
    guard,
    queue,
    sink: async (chunk) => {
      tracker.apply(chunk);
      await sleep(1);
    },
  });

  assert.equal(tracker.completed, true);
  assert.equal(tracker.rows, 100_000);
  assert.equal(stats.totalChunks, 200);
  assert.ok(stats.spilledBytes > 0);
});
