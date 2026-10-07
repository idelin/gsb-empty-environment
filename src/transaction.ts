import { TextDocument } from './document';
import { InverseEdit, PositionMap, TextEdit } from './types';

export interface TransactionOp {
  document: TextDocument;
  /** The frozen version the edits were computed against. */
  expectVersion: number;
  /** Non-overlapping edits, applied simultaneously. */
  edits: TextEdit[];
}

export interface TransactionSuccess {
  ok: true;
  /** Per-document results, in op order. */
  changes: {
    document: TextDocument;
    edits: TextEdit[];
    inverse: InverseEdit[];
    map: PositionMap;
    fromVersion: number;
    toVersion: number;
  }[];
  /** Reverse the whole transaction, restoring texts and versions. */
  undo(): void;
}

export interface TransactionConflict {
  document: TextDocument;
  expectVersion: number;
  actualVersion: number;
}

export interface TransactionFailure {
  ok: false;
  conflicts: TransactionConflict[];
}

export type TransactionResult = TransactionSuccess | TransactionFailure;

/**
 * A multi-document transaction. All operations target frozen versions
 * captured at `beginTransaction` time; `commit` applies every document's
 * edits only if every document is still at its expected version. If any
 * document changed concurrently, the entire batch is rejected and no
 * document is modified.
 */
export class Transaction {
  private readonly snapshots: { document: TextDocument; text: string; version: number }[];
  private committed = false;

  constructor(private readonly ops: TransactionOp[]) {
    this.snapshots = ops.map((op) => ({
      document: op.document,
      text: op.document.text,
      version: op.document.version,
    }));
  }

  commit(): TransactionResult {
    if (this.committed) {
      throw new Error('transaction already committed');
    }
    const conflicts: TransactionConflict[] = [];
    for (const op of this.ops) {
      const actual = op.document.version;
      if (actual !== op.expectVersion) {
        conflicts.push({ document: op.document, expectVersion: op.expectVersion, actualVersion: actual });
      }
    }
    if (conflicts.length > 0) {
      return { ok: false, conflicts };
    }
    this.committed = true;
    const snapshots = this.snapshots;
    const changes = this.ops.map((op) => {
      const applied = op.document.applyEdits(op.edits);
      return { document: op.document, ...applied };
    });
    return {
      ok: true,
      changes,
      undo: () => {
        for (const snap of snapshots) {
          snap.document.restore(snap.text, snap.version);
        }
      },
    };
  }
}

export function beginTransaction(ops: TransactionOp[]): Transaction {
  return new Transaction(ops);
}
