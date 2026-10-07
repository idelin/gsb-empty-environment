/** A half-open range of UTF-16 code-unit offsets: [start, end). */
export interface TextRange {
  /** Zero-based start offset, inclusive. */
  start: number;
  /** Zero-based end offset, exclusive. */
  end: number;
}

/**
 * A single text replacement. Ranges of all edits in one batch entry are
 * expressed in the coordinates of the document *before* the batch is
 * applied ("original coordinates") and must not overlap.
 */
export interface Edit {
  range: TextRange;
  /** Text that replaces the range. Empty string means deletion. */
  newText: string;
}

/** A set of edits applied atomically to one document at an expected version. */
export interface DocumentEdit {
  docId: string;
  /** The batch is rejected unless the document is exactly at this version. */
  expectedVersion: number;
  edits: Edit[];
}

/** Immutable view of a document's state. */
export interface DocumentSnapshot {
  docId: string;
  version: number;
  text: string;
}

/** Per-document record of what a committed batch changed. */
export interface BatchRecordEntry {
  docId: string;
  beforeVersion: number;
  afterVersion: number;
  beforeText: string;
  afterText: string;
}

/**
 * Proof that a batch was committed. Passed to `DocumentStore.undo` to
 * revert the batch; undo only succeeds while every affected document is
 * still at `afterVersion`, so later edits are never overwritten.
 */
export interface BatchReceipt {
  id: number;
  entries: BatchRecordEntry[];
  undone: boolean;
}
