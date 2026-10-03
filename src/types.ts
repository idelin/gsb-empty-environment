/** A single row-level change captured from the source database. */
export interface ChangeEvent {
  /** Monotonic log sequence number inside the transaction. */
  lsn: number;
  op: 'INSERT' | 'UPDATE' | 'DELETE';
  table: string;
  key: string;
  payload: string;
}

/**
 * Phase marker attached to every chunk so the receiver can track
 * whole-transaction integrity without holding all rows in memory.
 *
 *  BEGIN    - first data chunk of the transaction
 *  CONTINUE - any subsequent data chunk
 *  END      - terminal marker chunk (rowCount = 0) carrying final totals
 */
export type ChunkPhase = 'BEGIN' | 'CONTINUE' | 'END';

export interface ChunkHeader {
  txnId: string;
  chunkIndex: number;
  phase: ChunkPhase;
  /** Rows carried by this chunk (0 for the END marker). */
  rowCount: number;
  /** Rows seen so far in the transaction, including this chunk. */
  cumulativeRows: number;
  /** SHA-256 of this chunk's canonical row serialization. */
  checksum: string;
  /** Only set on the END marker. */
  totalRows?: number;
  totalChunks?: number;
  /** Running transaction checksum (chain of chunk checksums), only on END. */
  txnChecksum?: string;
}

export interface Chunk {
  header: ChunkHeader;
  rows: ChangeEvent[];
}
