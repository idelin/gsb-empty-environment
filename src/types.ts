export type LSN = number;
export type TxnId = number;
export type PageId = string;

export interface TxnTableEntry {
  txn: TxnId;
  status: string;
  lastLSN: LSN;
  firstLSN: LSN;
}

export interface DPTEntry {
  page: PageId;
  recLSN: LSN;
}

export type LogRecord =
  | { type: "update"; lsn: LSN; txn: TxnId; page: PageId; before: number; after: number; prevLSN: LSN }
  | { type: "clr"; lsn: LSN; txn: TxnId; page: PageId; before: number; after: number; prevLSN: LSN; undoNext: LSN }
  | { type: "commit"; lsn: LSN; txn: TxnId; prevLSN: LSN }
  | { type: "abort"; lsn: LSN; txn: TxnId; prevLSN: LSN }
  | { type: "end"; lsn: LSN; txn: TxnId; prevLSN: LSN }
  | { type: "chkpt_begin"; lsn: LSN }
  | { type: "chkpt_end"; lsn: LSN; beginLSN: LSN; txns: TxnTableEntry[]; dirtyPages: DPTEntry[] };

type WithoutLSN<T> = T extends unknown ? Omit<T, "lsn"> : never;
export type NewLogRecord = WithoutLSN<LogRecord>;

export type PageRecord = Extract<LogRecord, { type: "update" | "clr" }>;
