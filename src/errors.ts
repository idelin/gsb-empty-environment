export type BatchErrorCode =
  | 'DOCUMENT_NOT_FOUND'
  | 'DUPLICATE_DOCUMENT'
  | 'VERSION_MISMATCH'
  | 'EMPTY_EDITS'
  | 'INVALID_RANGE'
  | 'OVERLAPPING_EDITS'
  | 'UNDO_VERSION_MISMATCH';

export interface BatchError {
  code: BatchErrorCode;
  documentId: string;
  message: string;
  /** Index of the offending edit within its (sorted) change, when applicable. */
  editIndex?: number;
}

/**
 * Thrown when any part of a batch (or an undo) fails validation.
 * Carries every detected problem; nothing was modified.
 */
export class BatchValidationError extends Error {
  readonly errors: BatchError[];

  constructor(errors: BatchError[]) {
    super(
      `Batch rejected with ${errors.length} error(s): ` +
        errors.map((e) => `[${e.code}] ${e.documentId}: ${e.message}`).join('; '),
    );
    this.name = 'BatchValidationError';
    this.errors = errors;
  }
}
