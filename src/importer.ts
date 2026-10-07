import { stableBatchId, chunk } from './batch';
import { CancellationToken } from './cancellation';
import { parseRows } from './parser';
import { ImportStorage } from './storage';
import { BatchReport, ImportReport } from './types';
import { validateRows } from './validator';

export interface ImportOptions {
  storage: ImportStorage;
  batchSize?: number;
  token?: CancellationToken;
}

const DEFAULT_BATCH_SIZE = 100;

/**
 * parse -> validate -> batch -> commit.
 *
 * Cancellation contract:
 * - Cancelled before a batch's commit starts: that batch (and all later
 *   ones) never touch storage — zero visible writes from them.
 * - Cancelled while a commit is in flight: the in-flight batch runs to
 *   completion and its real storage result is reported; later batches stop.
 *
 * Resume contract: batch ids are content-derived and stable, so batches
 * already present in storage are skipped instead of re-committed.
 */
export async function runImport(source: string, options: ImportOptions): Promise<ImportReport> {
  const { storage, token } = options;
  const batchSize = options.batchSize ?? DEFAULT_BATCH_SIZE;

  const parsed = parseRows(source);
  const validated = validateRows(parsed.rows);
  const errors = [...parsed.errors, ...validated.errors].sort((a, b) => a.line - b.line);

  const batches = chunk(validated.valid, batchSize).map((rows) => ({
    id: stableBatchId(rows),
    rows,
  }));

  const reports: BatchReport[] = [];
  let cancelled = false;
  let committedRows = 0;
  let skippedRows = 0;

  for (const batch of batches) {
    if (token && token.cancelled) {
      cancelled = true;
      reports.push({ id: batch.id, status: 'not-started', rowCount: batch.rows.length });
      continue;
    }
    if (await storage.hasBatch(batch.id)) {
      skippedRows += batch.rows.length;
      reports.push({ id: batch.id, status: 'skipped', rowCount: batch.rows.length });
      continue;
    }
    // Re-check right before the point of no return: a cancel that landed
    // while hasBatch was in flight must still yield zero visible writes.
    if (token && token.cancelled) {
      cancelled = true;
      reports.push({ id: batch.id, status: 'not-started', rowCount: batch.rows.length });
      continue;
    }
    // Point of no return: once commitBatch is invoked it runs to completion,
    // even if cancellation is requested while it is in flight.
    const result = await storage.commitBatch(batch.id, batch.rows);
    committedRows += result.inserted;
    reports.push({ id: batch.id, status: 'committed', rowCount: batch.rows.length, result });
  }

  return {
    cancelled,
    committedRows,
    skippedRows,
    batches: reports,
    errors,
  };
}
