import { BatchValidationError, UndoConflictError } from './errors.ts';
import type {
  BatchReceipt,
  BatchRecordEntry,
  DocumentEdit,
  DocumentSnapshot,
  Edit,
  UndoConflict,
  ValidationIssue,
} from './types.ts';

interface DocState {
  text: string;
  version: number;
}

/**
 * Applies edits expressed in original (pre-batch) coordinates. The edits
 * must already be validated as non-overlapping. Applying from the highest
 * offset downwards keeps lower offsets valid without any shifting.
 */
function applyEditsInOriginalCoordinates(text: string, edits: Edit[]): string {
  const ordered = [...edits].sort((a, b) => b.range.start - a.range.start);
  let result = text;
  for (const edit of ordered) {
    result =
      result.slice(0, edit.range.start) + edit.newText + result.slice(edit.range.end);
  }
  return result;
}

/**
 * An in-memory document store supporting atomic multi-document batches.
 *
 * `applyBatch` works in two phases: it first validates every document's
 * expected version and every edit range against the pre-batch state, and
 * only then computes and commits all new texts. A rejected batch leaves
 * every document untouched.
 */
export class DocumentStore {
  private readonly docs = new Map<string, DocState>();
  private nextBatchId = 1;

  /** Creates a new empty document at version 0. */
  createDocument(docId: string, text = ''): DocumentSnapshot {
    if (this.docs.has(docId)) {
      throw new Error(`Document "${docId}" already exists`);
    }
    this.docs.set(docId, { text, version: 0 });
    return { docId, version: 0, text };
  }

  getSnapshot(docId: string): DocumentSnapshot {
    const state = this.docs.get(docId);
    if (state === undefined) {
      throw new Error(`Unknown document "${docId}"`);
    }
    return { docId, version: state.version, text: state.text };
  }

  /** Snapshots of all documents, sorted by docId for deterministic comparison. */
  listSnapshots(): DocumentSnapshot[] {
    return [...this.docs.keys()]
      .sort()
      .map((docId) => this.getSnapshot(docId));
  }

  /**
   * Validates the whole batch, then atomically publishes it.
   *
   * @throws {BatchValidationError} if any version or range check fails;
   *         no document is modified in that case.
   */
  applyBatch(batch: DocumentEdit[]): BatchReceipt {
    const issues = this.validateBatch(batch);
    if (issues.length > 0) {
      throw new BatchValidationError(issues);
    }

    // Pure computation of the post-batch state; cannot fail after validation.
    const entries: BatchRecordEntry[] = batch.map((documentEdit) => {
      const state = this.docs.get(documentEdit.docId);
      if (state === undefined) {
        throw new Error('unreachable: validated batch references unknown document');
      }
      const afterText = applyEditsInOriginalCoordinates(state.text, documentEdit.edits);
      return {
        docId: documentEdit.docId,
        beforeVersion: state.version,
        afterVersion: state.version + 1,
        beforeText: state.text,
        afterText,
      };
    });

    // Commit phase: single synchronous pass, no partial publication.
    for (const entry of entries) {
      this.docs.set(entry.docId, { text: entry.afterText, version: entry.afterVersion });
    }

    return { id: this.nextBatchId++, entries, undone: false };
  }

  /**
   * Reverts a previously committed batch. Every affected document must
   * still be at the version the batch produced; otherwise the undo is
   * rejected so that later edits are never overwritten. Undo is atomic:
   * either all documents are restored or none are.
   *
   * @throws {UndoConflictError} if any document has moved on; nothing is modified.
   */
  undo(receipt: BatchReceipt): void {
    if (receipt.undone) {
      throw new Error(`Batch ${receipt.id} has already been undone`);
    }

    const conflicts: UndoConflict[] = [];
    for (const entry of receipt.entries) {
      const state = this.docs.get(entry.docId);
      if (state === undefined || state.version !== entry.afterVersion) {
        conflicts.push({
          docId: entry.docId,
          expectedVersion: entry.afterVersion,
          actualVersion: state?.version,
        });
      }
    }
    if (conflicts.length > 0) {
      throw new UndoConflictError(conflicts);
    }

    for (const entry of receipt.entries) {
      this.docs.set(entry.docId, { text: entry.beforeText, version: entry.beforeVersion });
    }
    receipt.undone = true;
  }

  private validateBatch(batch: DocumentEdit[]): ValidationIssue[] {
    const issues: ValidationIssue[] = [];
    const seen = new Set<string>();

    for (const documentEdit of batch) {
      const { docId, expectedVersion, edits } = documentEdit;

      if (seen.has(docId)) {
        issues.push({
          kind: 'duplicate-document',
          docId,
          message: `Document "${docId}" appears more than once in the batch`,
        });
        continue;
      }
      seen.add(docId);

      const state = this.docs.get(docId);
      if (state === undefined) {
        issues.push({
          kind: 'unknown-document',
          docId,
          message: `Unknown document "${docId}"`,
        });
        continue;
      }

      if (expectedVersion !== state.version) {
        issues.push({
          kind: 'version-mismatch',
          docId,
          expectedVersion,
          actualVersion: state.version,
          message:
            `Document "${docId}" is at version ${state.version}, ` +
            `expected ${expectedVersion}`,
        });
      }

      issues.push(...this.validateEdits(docId, state.text, edits));
    }

    return issues;
  }

  private validateEdits(docId: string, text: string, edits: Edit[]): ValidationIssue[] {
    const issues: ValidationIssue[] = [];

    edits.forEach((edit, editIndex) => {
      const { start, end } = edit.range;
      if (!Number.isInteger(start) || !Number.isInteger(end) || start < 0 || end < start) {
        issues.push({
          kind: 'invalid-range',
          docId,
          editIndex,
          message:
            `Document "${docId}" edit #${editIndex} has invalid range ` +
            `[${String(start)}, ${String(end)})`,
        });
        return;
      }
      if (end > text.length) {
        issues.push({
          kind: 'range-out-of-bounds',
          docId,
          editIndex,
          message:
            `Document "${docId}" edit #${editIndex} range [${start}, ${end}) ` +
            `exceeds document length ${text.length}`,
        });
      }
    });

    // Overlap check in original coordinates, independent of input order.
    const ordered = edits
      .map((edit, editIndex) => ({ edit, editIndex }))
      .filter(
        ({ edit }) =>
          Number.isInteger(edit.range.start) &&
          Number.isInteger(edit.range.end) &&
          edit.range.start >= 0 &&
          edit.range.end >= edit.range.start,
      )
      .sort((a, b) => a.edit.range.start - b.edit.range.start);

    for (let i = 1; i < ordered.length; i += 1) {
      const previous = ordered[i - 1];
      const current = ordered[i];
      // Edits must have strictly increasing, non-overlapping ranges. Two
      // edits sharing a start offset are rejected even when one is empty:
      // their relative effect would depend on application order.
      if (
        current.edit.range.start < previous.edit.range.end ||
        current.edit.range.start === previous.edit.range.start
      ) {
        issues.push({
          kind: 'overlapping-edits',
          docId,
          editIndex: current.editIndex,
          message:
            `Document "${docId}" edits #${previous.editIndex} and #${current.editIndex} ` +
            `overlap in original coordinates`,
        });
      }
    }

    return issues;
  }
}
