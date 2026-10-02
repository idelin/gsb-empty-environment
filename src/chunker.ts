import { createHasher } from './checksum.ts';
import { MARKER_BYTES } from './types.ts';
import type { ChangeRow, Chunk } from './types.ts';

/**
 * 大事务分片器。
 *
 * 从异步行源中流式读取变更行，按 chunkSize 切分为带阶段标记的子分片：
 *   BEGIN(seq 0) -> DATA(seq 1..n) -> END(seq n+1, 携带总行数/总分片数/总校验和)
 *
 * 行源只需是可迭代流，分片器自身任意时刻只持有一个分片的行数据，
 * 因此百万级行变更不会一次性进入内存。
 */
export async function* splitTransaction(
  txId: string,
  rows: AsyncIterable<ChangeRow>,
  chunkSize: number,
): AsyncGenerator<Chunk> {
  if (!Number.isInteger(chunkSize) || chunkSize < 1) {
    throw new RangeError(`chunkSize 必须为正整数，收到: ${chunkSize}`);
  }

  let seq = 0;
  yield { kind: 'BEGIN', txId, seq: seq++, byteSize: MARKER_BYTES };

  const txHasher = createHasher();
  const chunkHasher = createHasher();
  let totalRows = 0;
  let totalChunks = 0;
  let batch: ChangeRow[] = [];
  let batchBytes = 0;

  const flush = (): Chunk => {
    const chunk: Chunk = {
      kind: 'DATA',
      txId,
      seq: seq++,
      rows: batch,
      rowCount: batch.length,
      checksum: chunkHasher.digest(),
      byteSize: batchBytes,
    };
    totalChunks++;
    batch = [];
    batchBytes = 0;
    chunkHasher.reset();
    return chunk;
  };

  for await (const row of rows) {
    const serialized = JSON.stringify(row);
    txHasher.update(serialized);
    chunkHasher.update(serialized);
    batch.push(row);
    batchBytes += Buffer.byteLength(serialized);
    totalRows++;
    if (batch.length >= chunkSize) {
      yield flush();
    }
  }
  if (batch.length > 0) {
    yield flush();
  }

  yield {
    kind: 'END',
    txId,
    seq: seq++,
    totalRows,
    totalChunks,
    checksum: txHasher.digest(),
    byteSize: MARKER_BYTES,
  };
}
