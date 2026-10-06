import { BatchValidationError, type BatchError } from './errors.ts';
import type {
  BatchReceipt,
  BatchReceiptEntry,
  DocumentChange,
  DocumentState,
  TextEdit,
  WorkspaceChangeEvent,
  WorkspaceListener,
  WorkspaceSnapshot,
} from './types.ts';

interface MutableDocument {
  id: string;
  text: string;
  version: number;
}

interface PreparedChange {
  document: MutableDocument;
  edits: TextEdit[];
  nextText: string;
}

function sortEdits(edits: readonly TextEdit[]): TextEdit[] {
  return [...edits].sort((a, b) => a.start - b.start || a.end - b.end);
}

function isInsertion(edit: TextEdit): boolean {
  return edit.start === edit.end;
}

/** Apply edits expressed in original coordinates, from last to first. */
function applyEditsToText(text: string, sortedEdits: readonly TextEdit[]): string {
  let result = text;
  for (let i = sortedEdits.length - 1; i >= 0; i -= 1) {
    const edit = sortedEdits[i];
    result = result.slice(0, edit.start) + edit.newText + result.slice(edit.end);
  }
  return result;
}

/**
 * Holds text documents and commits multi-document edit batches atomically:
 * every version and range is validated before anything is published, and a
 * failed batch leaves all documents untouched.
 */
export class Workspace {
  private readonly documents = new Map<string, MutableDocument>();
  private readonly listeners = new Set<WorkspaceListener>();
  private nextBatchId = 1;

  openDocument(id: string, text: string): DocumentState {
    if (this.documents.has(id)) {
      throw new Error(`Document already open: ${id}`);
    }
    const document: MutableDocument = { id, text, version: 1 };
    this.documents.set(id, document);
    return { ...document };
  }

  closeDocument(id: string): boolean {
    return this.documents.delete(id);
  }

  getDocument(id: string): DocumentState | undefined {
    const document = this.documents.get(id);
    return document ? { ...document } : undefined;
  }

  /** Deep-copied, serializable view of every document (snapshot model). */
  snapshot(): WorkspaceSnapshot {
    const snap: WorkspaceSnapshot = {};
    for (const [id, document] of [...this.documents.entries()].sort()) {
      snap[id] = { ...document };
    }
    return snap;
  }

  onDidChange(listener: WorkspaceListener): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /**
   * Validate every change (existence, version, ranges, non-overlap), then
   * commit all of them at once. Throws {@link BatchValidationError} without
   * modifying any document if anything is invalid.
   */
  applyBatch(changes: readonly DocumentChange[]): BatchReceipt {
    const prepared = this.prepare(changes);

    const batchId = this.nextBatchId++;
    const entries: BatchReceiptEntry[] = prepared.map(({ document, edits, nextText }) => {
      const entry: BatchReceiptEntry = {
        documentId: document.id,
        baseVersion: document.version,
        resultVersion: document.version + 1,
        previousText: document.text,
        appliedText: nextText,
        edits,
      };
      document.text = nextText;
      document.version += 1;
      return entry;
    });

    this.publish({ batchId, reason: 'apply', changes: entries });
    return { id: batchId, entries };
  }

  /**
   * Revert a committed batch. Every document must still be at the version the
   * batch produced, so a later edit anywhere rejects the whole undo and no
   * document is modified.
   */
  undo(receipt: BatchReceipt): void {
    const errors: BatchError[] = [];
    for (const entry of receipt.entries) {
      const document = this.documents.get(entry.documentId);
      if (!document) {
        errors.push({
          code: 'DOCUMENT_NOT_FOUND',
          documentId: entry.documentId,
          message: 'cannot undo: document is no longer open',
        });
      } else if (document.version !== entry.resultVersion) {
        errors.push({
          code: 'UNDO_VERSION_MISMATCH',
          documentId: entry.documentId,
          message:
            `cannot undo: document is at version ${document.version}, ` +
            `expected ${entry.resultVersion} (later edits would be overwritten)`,
        });
      }
    }
    if (errors.length > 0) {
      throw new BatchValidationError(errors);
    }

    for (const entry of receipt.entries) {
      const document = this.documents.get(entry.documentId)!;
      document.text = entry.previousText;
      document.version = entry.baseVersion;
    }

    this.publish({ batchId: receipt.id, reason: 'undo', changes: receipt.entries });
  }

  /** Phase 1: validate everything and compute resulting texts, mutating nothing. */
  private prepare(changes: readonly DocumentChange[]): PreparedChange[] {
    const errors: BatchError[] = [];
    const seen = new Set<string>();
    const prepared: PreparedChange[] = [];

    for (const change of changes) {
      if (seen.has(change.documentId)) {
        errors.push({
          code: 'DUPLICATE_DOCUMENT',
          documentId: change.documentId,
          message: 'document appears twice in the same batch',
        });
        continue;
      }
      seen.add(change.documentId);

      const document = this.documents.get(change.documentId);
      if (!document) {
        errors.push({
          code: 'DOCUMENT_NOT_FOUND',
          documentId: change.documentId,
          message: 'document is not open in this workspace',
        });
        continue;
      }

      if (document.version !== change.expectedVersion) {
        errors.push({
          code: 'VERSION_MISMATCH',
          documentId: change.documentId,
          message: `expected version ${change.expectedVersion}, actual version ${document.version}`,
        });
        continue;
      }

      if (change.edits.length === 0) {
        errors.push({
          code: 'EMPTY_EDITS',
          documentId: change.documentId,
          message: 'a change must contain at least one edit',
        });
        continue;
      }

      const edits = sortEdits(change.edits);
      const rangeError = this.validateRanges(change.documentId, document.text, edits);
      if (rangeError) {
        errors.push(rangeError);
        continue;
      }

      const overlapError = this.validateNonOverlap(change.documentId, edits);
      if (overlapError) {
        errors.push(overlapError);
        continue;
      }

      prepared.push({ document, edits, nextText: applyEditsToText(document.text, edits) });
    }

    if (errors.length > 0) {
      throw new BatchValidationError(errors);
    }
    return prepared;
  }

  private validateRanges(
    documentId: string,
    text: string,
    edits: readonly TextEdit[],
  ): BatchError | undefined {
    for (let i = 0; i < edits.length; i += 1) {
      const { start, end } = edits[i];
      const valid =
        Number.isInteger(start) &&
        Number.isInteger(end) &&
        start >= 0 &&
        end >= start &&
        end <= text.length;
      if (!valid) {
        return {
          code: 'INVALID_RANGE',
          documentId,
          editIndex: i,
          message: `edit [${start}, ${end}) is outside the document of length ${text.length}`,
        };
      }
    }
    return undefined;
  }

  private validateNonOverlap(documentId: string, edits: readonly TextEdit[]): BatchError | undefined {
    for (let i = 1; i < edits.length; i += 1) {
      const prev = edits[i - 1];
      const next = edits[i];
      const overlaps = next.start < prev.end;
      const ambiguousInserts =
        next.start === prev.end && isInsertion(prev) && isInsertion(next);
      if (overlaps || ambiguousInserts) {
        return {
          code: 'OVERLAPPING_EDITS',
          documentId,
          editIndex: i,
          message:
            `edit [${next.start}, ${next.end}) overlaps ` +
            `[${prev.start}, ${prev.end}) in original coordinates`,
        };
      }
    }
    return undefined;
  }

  private publish(event: WorkspaceChangeEvent): void {
    for (const listener of this.listeners) {
      listener(event);
    }
  }
}
