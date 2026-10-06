/** A single text edit expressed in offsets of the document's *original* text. */
export interface TextEdit {
  /** Inclusive start offset into the original text. */
  start: number;
  /** Exclusive end offset into the original text. `start === end` is an insertion. */
  end: number;
  /** Replacement text for `[start, end)`. */
  newText: string;
}

/** A set of edits targeting one document at one expected version. */
export interface DocumentChange {
  documentId: string;
  /** The batch is rejected unless the document's current version equals this. */
  expectedVersion: number;
  /** Edits in original coordinates; must not overlap. Order is irrelevant. */
  edits: TextEdit[];
}

/** Immutable view of a document. */
export interface DocumentState {
  id: string;
  text: string;
  version: number;
}

/** Serializable snapshot of the whole workspace, used by the snapshot model. */
export type WorkspaceSnapshot = Record<string, DocumentState>;

/** One document's record inside a committed batch receipt. */
export interface BatchReceiptEntry {
  documentId: string;
  /** Version before the batch (validated against `expectedVersion`). */
  baseVersion: number;
  /** Version after the batch (`baseVersion + 1`). */
  resultVersion: number;
  /** Full text before the batch; restored by `undo`. */
  previousText: string;
  /** Full text after the batch. */
  appliedText: string;
  /** Edits as applied, sorted ascending by start offset. */
  edits: TextEdit[];
}

/** Proof that a batch committed; required (and consumed) by `undo`. */
export interface BatchReceipt {
  id: number;
  entries: BatchReceiptEntry[];
}

export interface WorkspaceChangeEvent {
  batchId: number;
  reason: 'apply' | 'undo';
  changes: BatchReceiptEntry[];
}

export type WorkspaceListener = (event: WorkspaceChangeEvent) => void;
