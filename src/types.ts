export interface CsvRecord {
  [column: string]: string;
}

export interface ParsedRow {
  line: number;
  record: CsvRecord;
}

export interface RowViolation {
  line: number;
  field: string | null;
  message: string;
}

export interface ValidatedRow {
  line: number;
  record: CsvRecord;
}

export interface RejectedRow {
  line: number;
  record: CsvRecord;
  violations: RowViolation[];
}

export interface ValidationResult {
  accepted: ValidatedRow[];
  rejected: RejectedRow[];
}

export type BatchStatus = "committed" | "skipped";

export interface BatchResult {
  batchId: string;
  index: number;
  rowCount: number;
  status: BatchStatus;
}

export type CommitStatus = "committed" | "cancelled";

export interface CommitResult {
  status: CommitStatus;
  /** Batches executed during this run, in order, with their real outcomes. */
  batches: BatchResult[];
  /** Batch ids already committed before this run started (restart resume). */
  skippedBatchIds: string[];
  /** Number of rows written during this run. */
  committedRows: number;
  /** Batches never started because cancellation was requested. */
  remainingBatches: number;
}

/**
 * Durable sink for committed batches. Implementations must treat batchId as
 * an idempotency key: writing the same batchId twice must not duplicate rows.
 */
export interface Storage {
  writeBatch(batchId: string, rows: ValidatedRow[]): Promise<void>;
}

/** Durable record of which batches have been committed for an import. */
export interface CheckpointStore {
  loadCommittedBatchIds(importId: string): Promise<Set<string>>;
  markBatchCommitted(importId: string, batchId: string): Promise<void>;
}
