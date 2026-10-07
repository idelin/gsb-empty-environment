import { CommitBatchResult, ValidRow } from './types';

/**
 * Storage contract. `commitBatch` must be atomic and idempotent per batchId:
 * either all rows of the batch become visible, or none do.
 */
export interface ImportStorage {
  hasBatch(batchId: string): Promise<boolean>;
  commitBatch(batchId: string, rows: ValidRow[]): Promise<CommitBatchResult>;
  committedBatchIds(): Promise<string[]>;
  allRows(): Promise<ValidRow[]>;
}
