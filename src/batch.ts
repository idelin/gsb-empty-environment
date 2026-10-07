import { createHash } from 'crypto';
import { ValidRow } from './types';

export function chunk<T>(items: T[], size: number): T[][] {
  if (size <= 0) {
    throw new Error('batch size must be positive');
  }
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) {
    out.push(items.slice(i, i + size));
  }
  return out;
}

/**
 * Deterministic batch id derived from batch content. Same input rows and
 * same batch size always yield the same id, so a restarted run can match
 * its batches against the ones already committed in storage.
 */
export function stableBatchId(rows: ValidRow[]): string {
  const canonical = rows.map((row) => `${row.id}${row.name}${row.quantity}`).join('\n');
  const digest = createHash('sha256').update(canonical, 'utf8').digest('hex').slice(0, 16);
  return `batch-${rows.length}-${digest}`;
}
