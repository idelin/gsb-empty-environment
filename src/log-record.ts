/**
 * ARIES log record model.
 *
 * Every record carries a monotonically increasing LSN (log sequence number)
 * and a per-transaction `prevLSN` pointer that threads all records of one
 * transaction into a reverse-linked chain used by Undo.
 */

export type LSN = number;

export type LogRecordType =
  | 'BEGIN'
  | 'UPDATE'
  | 'COMMIT'
  | 'ABORT'
  | 'END'
  | 'CLR'
  | 'CHECKPOINT';

export type TxnStatus = 'RUNNING' | 'COMMITTING' | 'ABORTING';

export interface CheckpointData {
  /** txnId -> lastLSN/status snapshot taken at checkpoint time. */
  activeTxns: Record<number, { lastLSN: LSN; status: TxnStatus }>;
  /** pageId -> recLSN (oldest update that dirtied the page). */
  dirtyPages: Record<number, LSN>;
}

export interface LogRecord {
  lsn: LSN;
  /** Previous LSN of the same transaction (0 = start of chain). */
  prevLSN: LSN;
  /** Owning transaction id (0 for transaction-less records e.g. CHECKPOINT). */
  txnId: number;
  type: LogRecordType;
  pageId?: number;
  offset?: number;
  /** Value before the update (used by Undo). */
  beforeImage?: string;
  /** Value after the update (used by Redo). For a CLR this is the
   *  compensation value, i.e. the before-image of the record it undoes. */
  afterImage?: string;
  /** CLR only: LSN of the next record to undo (skips already-undone work). */
  undoNextLSN?: LSN;
  /** CHECKPOINT only. */
  checkpoint?: CheckpointData;
}

export function formatRecord(rec: LogRecord): string {
  const base =
    `LSN=${String(rec.lsn).padStart(3)} ${rec.type.padEnd(10)}` +
    ` txn=${rec.txnId} prev=${rec.prevLSN}`;
  switch (rec.type) {
    case 'UPDATE':
    case 'CLR': {
      const undoNext =
        rec.type === 'CLR' ? ` undoNext=${rec.undoNextLSN ?? 0}` : '';
      return (
        `${base} page=${rec.pageId} off=${rec.offset}` +
        ` '${rec.beforeImage}'->'${rec.afterImage}'${undoNext}`
      );
    }
    case 'CHECKPOINT': {
      const cp = rec.checkpoint!;
      return (
        `${base} active={${Object.keys(cp.activeTxns).join(',')}}` +
        ` dirty={${Object.entries(cp.dirtyPages)
          .map(([p, l]) => `${p}@${l}`)
          .join(',')}}`
      );
    }
    default:
      return base;
  }
}
