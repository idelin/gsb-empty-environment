import { VersionedDocument } from "./chain.ts";
import type { TextEdit } from "./types.ts";

/**
 * A group of edits applied atomically and undoable as one unit.
 * `inverse` restores the pre-transaction text when applied in order.
 */
export interface Transaction {
  /** Document version after the transaction was applied. */
  readonly version: number;
  /** Forward edits, in application order (descending by start offset). */
  readonly edits: readonly TextEdit[];
  /** Inverse edits, in application order (descending by start offset). */
  readonly inverse: readonly TextEdit[];
}

/**
 * A VersionedDocument with transaction-based undo/redo.
 *
 * Undo and redo are themselves applied as ordinary edits, so the edit
 * chain stays append-only and version numbers never go backwards — a
 * stale completion response remains verifiable against the chain even
 * after undos.
 */
export class TransactionalEditor {
  readonly document: VersionedDocument;
  private readonly undoStack: Transaction[] = [];
  private readonly redoStack: Transaction[] = [];

  constructor(initialText = "") {
    this.document = new VersionedDocument(initialText);
  }

  get version(): number {
    return this.document.version;
  }

  getText(): string {
    return this.document.getText();
  }

  /**
   * Apply a batch of edits as a single undoable transaction.
   * Edits are expressed against the current version and must not overlap;
   * they are applied right-to-left so their offsets stay valid.
   */
  transact(edits: readonly TextEdit[]): Transaction {
    if (edits.length === 0) {
      throw new Error("a transaction needs at least one edit");
    }
    const ordered = [...edits].sort((a, b) => b.start - a.start);
    for (let i = 0; i < ordered.length - 1; i += 1) {
      if (ordered[i]!.start < ordered[i + 1]!.end) {
        throw new Error("transaction edits must not overlap");
      }
    }

    const text = this.document.getText();
    // Inverse edits are expressed in post-transaction coordinates: each
    // edit's inserted text is shifted right by the deltas of the edits
    // to its left. Compute positions left-to-right, then reverse so the
    // inverse applies right-to-left like the forward edits.
    const inverse: TextEdit[] = [];
    let shift = 0;
    for (const e of [...ordered].reverse()) {
      const start = e.start + shift;
      inverse.unshift({ start, end: start + e.text.length, text: text.slice(e.start, e.end) });
      shift += e.text.length - (e.end - e.start);
    }

    for (const e of ordered) this.document.applyEdit(e);

    const tx: Transaction = { version: this.document.version, edits: ordered, inverse };
    this.undoStack.push(tx);
    this.redoStack.length = 0;
    return tx;
  }

  /** Undo the most recent transaction as one unit. Returns false if nothing to undo. */
  undo(): boolean {
    const tx = this.undoStack.pop();
    if (!tx) return false;
    for (const e of tx.inverse) this.document.applyEdit(e);
    this.redoStack.push(tx);
    return true;
  }

  /** Redo the most recently undone transaction. Returns false if nothing to redo. */
  redo(): boolean {
    const tx = this.redoStack.pop();
    if (!tx) return false;
    for (const e of tx.edits) this.document.applyEdit(e);
    this.undoStack.push(tx);
    return true;
  }

  get canUndo(): boolean {
    return this.undoStack.length > 0;
  }

  get canRedo(): boolean {
    return this.redoStack.length > 0;
  }
}
