import { hashChunk, TxnChecksum } from './checksum.ts';
import type { ChangeEvent, Chunk } from './types.ts';

export interface ReceiverSummary {
  txnId: string;
  totalRows: number;
  totalChunks: number;
  txnChecksum: string;
  committed: boolean;
}

/**
 * Reassembles a chunked transaction and verifies its integrity
 * incrementally — rows are applied chunk-by-chunk and never retained,
 * so receiver memory stays flat regardless of transaction size.
 *
 * Verified invariants:
 *  - chunk order: BEGIN first, contiguous indices, END last
 *  - per-chunk checksum matches the rows received
 *  - cumulative row counts are consistent
 *  - END totals and transaction checksum match what was received
 */
export class TransactionReceiver {
  private readonly txnChecksum = new TxnChecksum();
  private expectedIndex = 0;
  private receivedRows = 0;
  private receivedChunks = 0;
  private started = false;
  private committed = false;
  private finalChecksum = '';

  private readonly txnId: string;
  private readonly apply: (rows: ChangeEvent[]) => void | Promise<void>;

  constructor(
    txnId: string,
    apply: (rows: ChangeEvent[]) => void | Promise<void>,
  ) {
    this.txnId = txnId;
    this.apply = apply;
  }

  async accept(chunk: Chunk): Promise<void> {
    const { header } = chunk;
    if (header.txnId !== this.txnId) {
      throw new Error(`unexpected txnId ${header.txnId}, want ${this.txnId}`);
    }
    if (this.committed) {
      throw new Error('transaction already committed');
    }
    if (header.chunkIndex !== this.expectedIndex) {
      throw new Error(
        `chunk index gap: got ${header.chunkIndex}, want ${this.expectedIndex}`,
      );
    }
    if (header.cumulativeRows !== this.receivedRows + header.rowCount) {
      throw new Error(
        `cumulative row mismatch at chunk ${header.chunkIndex}: ` +
          `got ${header.cumulativeRows}, want ${this.receivedRows + header.rowCount}`,
      );
    }

    if (header.phase === 'END') {
      if (!this.started) throw new Error('END before BEGIN');
      if (header.totalRows !== this.receivedRows) {
        throw new Error(`totalRows mismatch: got ${header.totalRows}, want ${this.receivedRows}`);
      }
      if (header.totalChunks !== this.receivedChunks) {
        throw new Error(
          `totalChunks mismatch: got ${header.totalChunks}, want ${this.receivedChunks}`,
        );
      }
      const digest = this.txnChecksum.digest();
      if (header.txnChecksum !== digest) {
        throw new Error(`txnChecksum mismatch: got ${header.txnChecksum}, want ${digest}`);
      }
      this.committed = true;
      this.finalChecksum = digest;
    } else {
      const expectedPhase = this.started ? 'CONTINUE' : 'BEGIN';
      if (header.phase !== expectedPhase) {
        throw new Error(`bad phase ${header.phase} at chunk ${header.chunkIndex}, want ${expectedPhase}`);
      }
      if (chunk.rows.length !== header.rowCount) {
        throw new Error(`rowCount mismatch at chunk ${header.chunkIndex}`);
      }
      const checksum = hashChunk(chunk.rows);
      if (checksum !== header.checksum) {
        throw new Error(`chunk checksum mismatch at chunk ${header.chunkIndex}`);
      }
      this.txnChecksum.addChunk(checksum);
      this.started = true;
      this.receivedRows += header.rowCount;
      this.receivedChunks += 1;
      await this.apply(chunk.rows);
    }
    this.expectedIndex += 1;
  }

  get summary(): ReceiverSummary {
    return {
      txnId: this.txnId,
      totalRows: this.receivedRows,
      totalChunks: this.receivedChunks,
      txnChecksum: this.finalChecksum,
      committed: this.committed,
    };
  }
}
