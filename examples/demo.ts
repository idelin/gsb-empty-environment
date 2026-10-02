/**
 * 运行样例：源库一次大更新产生 500,000 条变更行，
 * 传输器将其切分为 100 个批次流式发送，接收端逐片校验事务完整性，
 * 全程采样堆内存，验证峰值增量保持在 10MB 以内。
 *
 * 运行：npm run demo
 */
import { MemoryGuard } from '../src/memory-guard.ts';
import { heapBaseline, startMemoryMonitor } from '../src/memory-monitor.ts';
import { SpillQueue } from '../src/spill-queue.ts';
import { TransactionTracker } from '../src/tracker.ts';
import { transportTransaction } from '../src/transporter.ts';
import type { ChangeRow, Chunk } from '../src/types.ts';

const ROW_COUNT = 500_000;
const BATCH_COUNT = 100;
const CHUNK_SIZE = ROW_COUNT / BATCH_COUNT;
const MEMORY_BUDGET_BYTES = 10 * 1024 * 1024;

async function* sourceRows(): AsyncGenerator<ChangeRow> {
  for (let i = 0; i < ROW_COUNT; i++) {
    yield {
      op: 'UPDATE',
      table: 'orders',
      pk: i,
      data: { status: 'SHIPPED', updated_at: '2026-10-02T00:00:00Z', version: i },
    };
  }
}

const fmtMB = (bytes: number): string => (bytes / 1024 / 1024).toFixed(2) + ' MB';
const fmtKB = (bytes: number): string => (bytes / 1024).toFixed(0) + ' KB';
const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

async function main(): Promise<void> {
  const txId = 'tx-demo-' + Date.now();
  const tracker = new TransactionTracker(txId);
  const guard = new MemoryGuard(2 * 1024 * 1024);
  const queue = new SpillQueue<Chunk>({
    maxMemoryBytes: 512 * 1024,
    serialize: (c) => Buffer.from(JSON.stringify(c), 'utf8'),
    deserialize: (b) => JSON.parse(b.toString('utf8')) as Chunk,
  });

  const baseline = await heapBaseline();
  const monitor = startMemoryMonitor(5);

  console.log('=== 大事务分片传输 Demo ===');
  console.log(`事务 ${txId}：${ROW_COUNT.toLocaleString()} 行变更 -> ${BATCH_COUNT} 个批次`);

  const stats = await transportTransaction(sourceRows(), {
    txId,
    chunkSize: CHUNK_SIZE,
    guard,
    queue,
    sink: async (chunk) => {
      tracker.apply(chunk);
      await sleep(1); // 模拟接收端应用变更的耗时
    },
  });

  const peakHeap = monitor.stop();
  const peakDelta = peakHeap - baseline;

  console.log('--- 传输统计 ---');
  console.log(`总行数        : ${stats.totalRows.toLocaleString()}`);
  console.log(`数据分片数    : ${stats.totalChunks}`);
  console.log(`在途字节峰值  : ${fmtKB(stats.peakInFlightBytes)} (限流上限 ${fmtMB(guard.limit)})`);
  console.log(`磁盘溢写字节  : ${fmtKB(stats.spilledBytes)}`);
  console.log(`耗时          : ${stats.durationMs} ms`);
  console.log('--- 内存指标 ---');
  console.log(`堆基线        : ${fmtMB(baseline)}`);
  console.log(`堆峰值        : ${fmtMB(peakHeap)}`);
  console.log(`峰值增量      : ${fmtMB(peakDelta)} (预算 ${fmtMB(MEMORY_BUDGET_BYTES)})`);
  console.log('--- 完整性 ---');
  console.log(`事务完整性    : ${tracker.completed ? '通过' : '失败'} (接收 ${tracker.rows.toLocaleString()} 行 / ${tracker.chunks} 分片)`);

  if (!tracker.completed) {
    throw new Error('事务完整性校验失败');
  }
  if (peakDelta >= MEMORY_BUDGET_BYTES) {
    throw new Error(`峰值内存增量 ${fmtMB(peakDelta)} 超出 10MB 预算`);
  }
  console.log('结果：验收通过 ✔');
}

await main();
