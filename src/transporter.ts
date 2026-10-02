import { splitTransaction } from './chunker.ts';
import type { MemoryGuard } from './memory-guard.ts';
import type { SpillQueue } from './spill-queue.ts';
import type { ChangeRow, Chunk } from './types.ts';

export interface TransportOptions {
  txId: string;
  /** 每个 DATA 分片包含的最大行数 */
  chunkSize: number;
  /** 内存保护限流器：约束在途字节数，形成背压 */
  guard: MemoryGuard;
  /** 分片缓冲队列：内存超额时自动溢写临时磁盘 */
  queue: SpillQueue<Chunk>;
  /** 接收端处理函数（如同步应用、转发到目标库等） */
  sink: (chunk: Chunk) => Promise<void>;
}

export interface TransportStats {
  txId: string;
  totalRows: number;
  totalChunks: number;
  /** 限流器观测到的在途字节峰值 */
  peakInFlightBytes: number;
  /** 溢写到磁盘的累计字节数 */
  spilledBytes: number;
  durationMs: number;
}

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

/**
 * 大事务分片传输管线。
 *
 * 生产者：从行源流式读取 -> 分片 -> acquire 限流额度 -> 入队（超额溢写磁盘）。
 * 消费者：从队列取出 -> 投递 sink -> release 限流额度。
 *
 * 在途字节数始终被 MemoryGuard 约束；消费端处理缓慢时，缓冲队列将分片
 * 溢写到临时磁盘，内存占用不会随事务规模增长，从而防止 OOM。
 */
export async function transportTransaction(
  rows: AsyncIterable<ChangeRow>,
  options: TransportOptions,
): Promise<TransportStats> {
  const { txId, chunkSize, guard, queue, sink } = options;
  const startedAt = Date.now();

  let produced = 0;
  let consumed = 0;
  let productionDone = false;
  let productionError: unknown = null;
  let totalRows = 0;
  let totalChunks = 0;

  const producer = (async (): Promise<void> => {
    try {
      for await (const chunk of splitTransaction(txId, rows, chunkSize)) {
        await guard.acquire(chunk.byteSize);
        await queue.push(chunk, chunk.byteSize);
        produced++;
        if (chunk.kind === 'DATA') {
          totalRows += chunk.rowCount;
          totalChunks++;
        }
      }
    } catch (err) {
      productionError = err;
    } finally {
      productionDone = true;
    }
  })();

  let consumerError: unknown = null;
  try {
    for (;;) {
      const chunk = await queue.shift();
      if (chunk === undefined) {
        if (productionDone && consumed >= produced) {
          break;
        }
        await sleep(1);
        continue;
      }
      consumed++;
      try {
        await sink(chunk);
      } finally {
        guard.release(chunk.byteSize);
      }
    }
  } catch (err) {
    consumerError = err;
  } finally {
    if (consumerError !== null) {
      // 消费端失败：中止限流器，解除生产者可能的背压阻塞，避免死锁
      guard.abort(consumerError);
    }
    // producer 内部自行捕获错误（含 abort 导致的拒绝），此处 await 不会 reject
    await producer;
    await queue.close();
  }

  if (consumerError !== null) {
    throw consumerError;
  }
  if (productionError !== null) {
    throw productionError;
  }

  return {
    txId,
    totalRows,
    totalChunks,
    peakInFlightBytes: guard.peakUsage,
    spilledBytes: queue.spilledBytes,
    durationMs: Date.now() - startedAt,
  };
}
