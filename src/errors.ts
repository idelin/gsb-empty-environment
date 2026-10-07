export type ValidationIssueKind =
  | 'unknown-document'
  | 'duplicate-document'
  | 'version-mismatch'
  | 'invalid-range'
  | 'range-out-of-bounds'
  | 'overlapping-edits';

export interface ValidationIssue {
  kind: ValidationIssueKind;
  docId: string;
  message: string;
  /** Index of the offending edit inside its DocumentEdit, when applicable. */
  editIndex?: number;
  expectedVersion?: number;
  actualVersion?: number;
}

/** Thrown when any part of a batch fails validation. Nothing is modified. */
export class BatchValidationError extends Error {
  readonly issues: readonly ValidationIssue[];

  constructor(issues: readonly ValidationIssue[]) {
    super(
      `Batch rejected with ${issues.length} issue(s): ` +
        issues.map((issue) => issue.message).join('; '),
    );
    this.name = 'BatchValidationError';
    this.issues = issues;
  }
}

export interface UndoConflict {
  docId: string;
  expectedVersion: number;
  actualVersion: number | undefined;
}

/**
 * Thrown when undo is attempted but at least one document has moved past
 * the version the batch produced. Nothing is modified.
 */
export class UndoConflictError extends Error {
  readonly conflicts: readonly UndoConflict[];

  constructor(conflicts: readonly UndoConflict[]) {
    super(
      `Undo rejected for ${conflicts.length} document(s): ` +
        conflicts
          .map(
            (c) =>
              `"${c.docId}" is at version ${String(c.actualVersion)}, expected ${c.expectedVersion}`,
          )
          .join('; '),
    );
    this.name = 'UndoConflictError';
    this.conflicts = conflicts;
  }
}
