/** Row image: column name -> value. */
export type RowImage = Record<string, unknown>;

/** CDC operation kinds. */
export type CdcOp = "INSERT" | "UPDATE" | "DELETE";

/**
 * A single CDC change event capturing the before/after image of a row.
 * - INSERT: before = null, after = new row
 * - UPDATE: before = old row, after = new row
 * - DELETE: before = old row, after = null
 */
export interface CdcEvent {
  /** Monotonically increasing sequence number in the log. */
  readonly seq: number;
  /** Event commit time, milliseconds since epoch (fractional ms allowed). */
  readonly tsMs: number;
  /** Transaction id that committed this change. */
  readonly txId: string;
  readonly table: string;
  readonly op: CdcOp;
  /** Primary key of the affected row. */
  readonly pk: string | number;
  readonly before: RowImage | null;
  readonly after: RowImage | null;
}

/** A single reverse-compensation DML operation. */
export interface CompensationOp {
  /** Sequence of the source CDC event this op compensates. */
  readonly sourceSeq: number;
  readonly table: string;
  readonly pk: string | number;
  /** Inverse of the source op: INSERT->DELETE, DELETE->INSERT, UPDATE->UPDATE(before image). */
  readonly op: CdcOp;
  /** Row image to restore (for INSERT / UPDATE compensation). */
  readonly image: RowImage | null;
  /** Rendered SQL statement. */
  readonly sql: string;
}

/** A rollback package: an ordered, replayable set of compensation ops. */
export interface RollbackPackage {
  readonly id: string;
  readonly createdAtMs: number;
  /** Human-readable reason, e.g. "rollback tx tx-123". */
  readonly reason: string;
  /** Ops ordered for replay (reverse of the original commit order). */
  readonly ops: readonly CompensationOp[];
  /** Inclusive seq range of the CDC events covered. */
  readonly coveredSeq: { from: number; to: number } | null;
}
