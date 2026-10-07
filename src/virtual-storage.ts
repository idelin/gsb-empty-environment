import { Gate } from './deferred';
import { ImportStorage } from './storage';
import { CommitBatchResult, ValidRow } from './types';

export interface VirtualStorageHooks {
  /** Runs before the existence check of each batch. */
  beforeHasBatch?: (batchId: string) => void | Promise<void>;
  /** Runs inside commitBatch, before the atomic apply. */
  beforeCommit?: (batchId: string, rows: ValidRow[]) => void | Promise<void>;
  /** Runs inside commitBatch, after the atomic apply. */
  afterCommit?: (batchId: string, result: CommitBatchResult) => void | Promise<void>;
}

/**
 * In-memory storage with injectable async hooks so tests can park the
 * importer at precise boundaries (e.g. cancel while a commit is in flight).
 */
export class VirtualStorage implements ImportStorage {
  private readonly batches = new Map<string, ValidRow[]>();
  readonly commitLog: string[] = [];

  constructor(private readonly hooks: VirtualStorageHooks = {}) {}

  async hasBatch(batchId: string): Promise<boolean> {
    if (this.hooks.beforeHasBatch) {
      await this.hooks.beforeHasBatch(batchId);
    }
    return this.batches.has(batchId);
  }

  async commitBatch(batchId: string, rows: ValidRow[]): Promise<CommitBatchResult> {
    if (this.hooks.beforeCommit) {
      await this.hooks.beforeCommit(batchId, rows);
    }
    // Atomic apply: rows become visible only as one unit.
    if (!this.batches.has(batchId)) {
      this.batches.set(batchId, rows.slice());
      this.commitLog.push(batchId);
    }
    const result: CommitBatchResult = { batchId, inserted: rows.length };
    if (this.hooks.afterCommit) {
      await this.hooks.afterCommit(batchId, result);
    }
    return result;
  }

  async committedBatchIds(): Promise<string[]> {
    return Array.from(this.batches.keys());
  }

  async allRows(): Promise<ValidRow[]> {
    const rows: ValidRow[] = [];
    for (const batchRows of this.batches.values()) {
      rows.push(...batchRows);
    }
    return rows;
  }
}

/** A gate-based hook set: commits block on `commitGate` until it is opened. */
export function gatedCommit(gate: Gate, onEnter?: (batchId: string) => void): VirtualStorageHooks {
  return {
    beforeCommit: (batchId) => {
      if (onEnter) {
        onEnter(batchId);
      }
      return gate.wait();
    },
  };
}
