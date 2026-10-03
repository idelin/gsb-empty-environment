import { chunkTransaction } from './chunker.ts';
import { SpillQueue, type SpillQueueStats } from './spill-queue.ts';
import type { TransactionReceiver } from './receiver.ts';
import type { ChangeEvent, Chunk } from './types.ts';

export interface TransportOptions {
  txnId: string;
  /** Lazily-produced change stream; never fully materialized. */
  source: AsyncIterable<ChangeEvent>;
  /** Rows per chunk. */
  chunkSize: number;
  /** Memory-protection rate limiter shared by producer and consumer. */
  guard: SpillQueue;
  receiver: TransactionReceiver;
  /** Called after each chunk is delivered (e.g. for memory sampling). */
  onChunkDelivered?: (chunk: Chunk) => void;
}

export interface TransportResult {
  chunks: number;
  rows: number;
  wireBytes: number;
  guard: SpillQueueStats;
}

export function serializeChunk(chunk: Chunk): Buffer {
  return Buffer.from(JSON.stringify(chunk), 'utf8');
}

export function deserializeChunk(buf: Buffer): Chunk {
  return JSON.parse(buf.toString('utf8')) as Chunk;
}

/**
 * Streams one transaction from source to receiver through the memory
 * guard: chunker (producer) and receiver (consumer) run concurrently,
 * connected only by the bounded SpillQueue, so peak memory is bounded
 * by the guard's budget plus one chunk on each side.
 */
export async function transportTransaction(
  options: TransportOptions,
): Promise<TransportResult> {
  const { txnId, source, chunkSize, guard, receiver, onChunkDelivered } = options;

  let chunks = 0;
  let rows = 0;
  let wireBytes = 0;

  const producer = (async () => {
    try {
      for await (const chunk of chunkTransaction(txnId, source, chunkSize)) {
        await guard.push(serializeChunk(chunk));
      }
    } finally {
      guard.close();
    }
  })();

  const consumer = (async () => {
    for (;;) {
      const buf = await guard.shift();
      if (buf === undefined) return;
      wireBytes += buf.length;
      const chunk = deserializeChunk(buf);
      await receiver.accept(chunk);
      chunks += 1;
      rows += chunk.header.rowCount;
      onChunkDelivered?.(chunk);
    }
  })();

  await Promise.all([producer, consumer]);
  return { chunks, rows, wireBytes, guard: guard.stats };
}
