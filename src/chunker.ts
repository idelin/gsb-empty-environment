import { hashChunk, TxnChecksum } from './checksum.ts';
import type { ChangeEvent, Chunk } from './types.ts';

/**
 * Splits one large transaction's change stream into fixed-size chunks.
 *
 * The source is consumed lazily (async iterable), so a transaction with
 * millions of row changes is never materialized in memory: at most
 * `chunkSize` rows are buffered at any moment.
 *
 * Emitted sequence: BEGIN chunk, zero or more CONTINUE chunks, then a
 * terminal END marker chunk carrying totalRows / totalChunks / txnChecksum
 * so the receiver can verify whole-transaction integrity.
 */
export async function* chunkTransaction(
  txnId: string,
  source: AsyncIterable<ChangeEvent>,
  chunkSize: number,
): AsyncGenerator<Chunk> {
  if (!Number.isInteger(chunkSize) || chunkSize <= 0) {
    throw new RangeError(`chunkSize must be a positive integer, got ${chunkSize}`);
  }

  const txnChecksum = new TxnChecksum();
  let chunkIndex = 0;
  let cumulativeRows = 0;
  let rows: ChangeEvent[] = [];

  const emitDataChunk = (): Chunk => {
    const checksum = hashChunk(rows);
    txnChecksum.addChunk(checksum);
    cumulativeRows += rows.length;
    const chunk: Chunk = {
      header: {
        txnId,
        chunkIndex,
        phase: chunkIndex === 0 ? 'BEGIN' : 'CONTINUE',
        rowCount: rows.length,
        cumulativeRows,
        checksum,
      },
      rows,
    };
    chunkIndex += 1;
    rows = [];
    return chunk;
  };

  for await (const event of source) {
    rows.push(event);
    if (rows.length === chunkSize) {
      yield emitDataChunk();
    }
  }
  if (rows.length > 0) {
    yield emitDataChunk();
  }

  yield {
    header: {
      txnId,
      chunkIndex,
      phase: 'END',
      rowCount: 0,
      cumulativeRows,
      checksum: '',
      totalRows: cumulativeRows,
      totalChunks: chunkIndex,
      txnChecksum: txnChecksum.digest(),
    },
    rows: [],
  };
}
