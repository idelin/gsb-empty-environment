import { compare3, makeKeyComparator } from './predicate.ts';
import type { SourceSpec } from './source.ts';
import type { QueryPlan, ResultRow, Value } from './types.ts';

interface FullRow {
  rowId: number;
  version: number;
  seq: number;
  cols: Record<string, Value>;
}

/**
 * Reference implementation: plain eager row-by-row evaluation.
 * Reads every column of every row up front, then filters, stably sorts and
 * projects. The pipeline under test must produce exactly the same rows.
 */
export function referenceExecute(spec: SourceSpec, plan: QueryPlan): ResultRow[] {
  const all: FullRow[] = [];
  let seq = 0;
  for (const batch of spec.batches) {
    for (let i = 0; i < batch.rowIds.length; i++) {
      const cols: Record<string, Value> = {};
      for (const [name, values] of Object.entries(batch.columns)) {
        cols[name] = values[i];
      }
      all.push({
        rowId: batch.rowIds[i],
        version: batch.rowVersions[i],
        seq: seq++,
        cols,
      });
    }
  }

  let rows = all;
  const filter = plan.filter;
  if (filter) {
    rows = rows.filter(
      (row) => compare3(row.cols[filter.column], filter.op, filter.value) === 'true',
    );
  }

  const sort = plan.sort;
  if (sort) {
    const compareKeys = makeKeyComparator(sort.direction ?? 'asc');
    const decorated = rows.map((row) => ({ row, key: row.cols[sort.column] }));
    decorated.sort(
      (a, b) => compareKeys(a.key, b.key) || a.row.seq - b.row.seq,
    );
    rows = decorated.map((entry) => entry.row);
  }

  return rows.map((row) => {
    const values: Record<string, Value> = {};
    for (const column of plan.project) values[column] = row.cols[column];
    return { rowId: row.rowId, version: row.version, values };
  });
}
