import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MemoryGuard } from '../src/memory-guard.ts';
import { heapBaseline, startMemoryMonitor } from '../src/memory-monitor.ts';
import { SpillQueue } from '../src/spill-queue.ts';
import { TransactionTracker } from '../src/tracker.ts';
import { transportTransaction } from '../src/transporter.ts';
import type { Chunk } from '../src/types.ts';
import { generateRows, sleep } from './helpers.ts';

const ROW_COUNT = 500_000;
const BATCH_COUNT = 100;
const CHUNK_SIZE = ROW_COUNT / BATCH_COUNT;
const MEMORY_BUDGET_BYTES = 10 * 1024 * 1024;

test(
  '验收：源库大更新产生 50 万条变更，切分为 100 个批次流式传输，峰值堆内存增量 < 10MB',
  { timeout: 120_000 },
  async () => {
    const tracker = new TransactionTracker('tx-acceptance');
    const guard = new MemoryGuard(2 * 1024 * 1024);
    const queue = new SpillQueue<Chunk>({
      maxMemoryBytes: 512 * 1024,
      serialize: (c) => Buffer.from(JSON.stringify(c), 'utf8'),
      deserialize: (b) => JSON.parse(b.toString('utf8')) as Chunk,
    });

    const baseline = await heapBaseline();
    const monitor = startMemoryMonitor(5);

    const stats = await transportTransaction(generateRows(ROW_COUNT), {
      txId: 'tx-acceptance',
      chunkSize: CHUNK_SIZE,
      guard,
      queue,
      sink: async (chunk) => {
        tracker.apply(chunk);
        await sleep(1); // 模拟接收端应用变更的耗时，制造背压
      },
    });

    const peakHeap = monitor.stop();
    const peakDelta = peakHeap - baseline;

    console.log(
      `    [验收指标] 行数=${stats.totalRows} 批次=${stats.totalChunks} ` +
        `在途峰值=${(stats.peakInFlightBytes / 1024).toFixed(0)}KB ` +
        `溢写磁盘=${(stats.spilledBytes / 1024).toFixed(0)}KB ` +
        `堆基线=${(baseline / 1024 / 1024).toFixed(2)}MB ` +
        `堆峰值=${(peakHeap / 1024 / 1024).toFixed(2)}MB ` +
        `峰值增量=${(peakDelta / 1024 / 1024).toFixed(2)}MB ` +
        `耗时=${stats.durationMs}ms`,
    );

    assert.equal(stats.totalRows, ROW_COUNT, '总变更行数必须为 50 万');
    assert.equal(stats.totalChunks, BATCH_COUNT, '必须切分为 100 个批次');
    assert.equal(tracker.completed, true, '事务完整性校验必须通过');
    assert.equal(tracker.rows, ROW_COUNT);
    assert.ok(
      peakDelta < MEMORY_BUDGET_BYTES,
      `峰值堆内存增量 ${(peakDelta / 1024 / 1024).toFixed(2)}MB 超出 10MB 预算`,
    );
  },
);
