import { CancellationToken } from "./cancellation";
import {
  BatchResult,
  CheckpointStore,
  CommitResult,
  Storage,
  ValidatedRow,
} from "./types";

export interface CommitOptions {
  /** Logical id of this import; batch ids derive from it deterministically. */
  importId: string;
  batchSize: number;
  cancellationToken?: CancellationToken;
}

/** Stable, deterministic batch id: same importId + index on every run. */
export function batchIdFor(importId: string, index: number): string {
  return `${importId}/batch/${String(index).padStart(6, "0")}`;
}

function chunk<T>(items: T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    out.push(items.slice(i, i + size));
  }
  return out;
}

/**
 * Commits validated rows in batches.
 *
 * Guarantees:
 * - Cancellation is only observed between batches. If the token is cancelled
 *   before the first batch starts, zero writes become visible. If it is
 *   cancelled while a batch is in flight, that batch finishes, is
 *   checkpointed, and its real result is returned; later batches never start.
 * - Batch ids are stable across runs, so a restarted run skips batches that
 *   a previous run already committed (loaded from the checkpoint store).
 */
export async function commitBatches(
  rows: ValidatedRow[],
  options: CommitOptions,
  storage: Storage,
  checkpoints: CheckpointStore
): Promise<CommitResult> {
  if (options.batchSize < 1) {
    throw new Error("batchSize must be >= 1");
  }
  const batches = chunk(rows, options.batchSize);
  const alreadyCommitted = await checkpoints.loadCommittedBatchIds(options.importId);

  const result: CommitResult = {
    status: "committed",
    batches: [],
    skippedBatchIds: [],
    committedRows: 0,
    remainingBatches: 0,
  };

  for (let index = 0; index < batches.length; index++) {
    const batchId = batchIdFor(options.importId, index);
    if (alreadyCommitted.has(batchId)) {
      result.skippedBatchIds.push(batchId);
      continue;
    }
    if (options.cancellationToken && options.cancellationToken.isCancellationRequested) {
      result.status = "cancelled";
      result.remainingBatches = batches.length - index;
      break;
    }
    const batch = batches[index];
    await storage.writeBatch(batchId, batch);
    await checkpoints.markBatchCommitted(options.importId, batchId);
    const batchResult: BatchResult = {
      batchId,
      index,
      rowCount: batch.length,
      status: "committed",
    };
    result.batches.push(batchResult);
    result.committedRows += batch.length;
  }

  return result;
}
