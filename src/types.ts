export type LSN = number;
export type TxnId = number;
export type PageId = number;

export const NULL_LSN: LSN = 0;

export const RecordType = {
  UPDATE: 'UPDATE',
  CLR: 'CLR',
  COMMIT: 'COMMIT',
  ABORT: 'ABORT',
  END: 'END',
  BEGIN_CHECKPOINT: 'BEGIN_CHECKPOINT',
  END_CHECKPOINT: 'END_CHECKPOINT',
} as const;
export type RecordType = (typeof RecordType)[keyof typeof RecordType];

export type TxnStatus = 'RUNNING' | 'COMMITTED' | 'ABORTING';

export interface TxnTableEntry {
  txnId: TxnId;
  status: TxnStatus;
  lastLSN: LSN;
}

export interface LogRecord {
  lsn: LSN;
  type: RecordType;
  txnId: TxnId;
  prevLSN: LSN;
  pageId?: PageId;
  before?: number;
  after?: number;
  undoNextLSN?: LSN;
  beginLSN?: LSN;
  txnTable?: TxnTableEntry[];
  dpt?: [PageId, LSN][];
}
