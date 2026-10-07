export interface RawRow {
  /** 1-based line number in the source text. */
  line: number;
  fields: string[];
  raw: string;
}

export interface ValidRow {
  line: number;
  id: string;
  name: string;
  quantity: number;
}

export interface RowError {
  line: number;
  reason: string;
  raw: string;
}

export interface CommitBatchResult {
  batchId: string;
  inserted: number;
}

export type BatchStatus = 'committed' | 'skipped' | 'not-started';

export interface BatchReport {
  id: string;
  status: BatchStatus;
  rowCount: number;
  /** Present for committed batches — the real result returned by storage. */
  result?: CommitBatchResult;
}

export interface ImportReport {
  cancelled: boolean;
  committedRows: number;
  skippedRows: number;
  batches: BatchReport[];
  errors: RowError[];
}
