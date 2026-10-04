import { SelectionVector } from './selection.ts';
import { compare3 } from './predicate.ts';
import type { RowBatch } from './batch.ts';
import type { FilterSpec } from './types.ts';

/**
 * Evaluate the filter against a narrow inline column and produce the
 * selection vector for this batch. Only rows whose predicate is 'true'
 * survive; 'false' and 'unknown' (null) are dropped.
 */
export function buildSelection(batch: RowBatch, filter: FilterSpec): SelectionVector {
  const view = batch.column(filter.column);
  const picked: number[] = [];
  for (let i = 0; i < batch.rowCount; i++) {
    if (compare3(view.value(i), filter.op, filter.value) === 'true') {
      picked.push(i);
    }
  }
  return SelectionVector.from(batch, Uint32Array.from(picked));
}

export function selectAll(batch: RowBatch): SelectionVector {
  return SelectionVector.from(
    batch,
    Uint32Array.from({ length: batch.rowCount }, (_, i) => i),
  );
}
