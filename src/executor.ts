import { ColumnVector } from './column.ts';
import { throwIfAborted } from './errors.ts';
import { buildSelection, selectAll } from './operators.ts';
import { makeKeyComparator } from './predicate.ts';
import type { RowBatch } from './batch.ts';
import type { BatchSource } from './source.ts';
import type { QueryPlan, ResultRow, RowRef, Value } from './types.ts';

export interface QueryResult {
  rows: ResultRow[];
  stats: Record<string, { calls: number; rowsLoaded: number }>;
}

interface Run {
  ref: RowRef;
  sortKey: Value;
  seq: number;
}

/**
 * Late-materializing pipeline: scan -> filter (narrow) -> stable sort -> project (wide).
 *
 * Phase 1 keeps only (rowRef, sortKey, seq) per surviving row and releases
 * each batch immediately; wide projected columns are fetched in phase 3,
 * only for batches that still have rows in the final output.
 */
export async function execute(
  source: BatchSource,
  plan: QueryPlan,
  signal?: AbortSignal,
): Promise<QueryResult> {
  const runs: Run[] = [];
  let seq = 0;

  // Phase 1: scan + filter on narrow columns; extract sort keys for survivors.
  for (;;) {
    throwIfAborted(signal);
    const batch = await source.acquire();
    if (batch === null) break;
    try {
      const selection = plan.filter ? buildSelection(batch, plan.filter) : selectAll(batch);
      const indices = selection.resolve(batch);
      if (indices.length > 0) {
        let sortKeys: ColumnVector | null = null;
        if (plan.sort) {
          sortKeys = await loadValues(source, batch, plan.sort.column, indices, signal);
        }
        for (let p = 0; p < indices.length; p++) {
          const i = indices[p];
          runs.push({
            ref: {
              batchId: batch.batchId,
              rowIndex: i,
              rowId: batch.rowId(i),
              version: batch.rowVersion(i),
            },
            sortKey: sortKeys ? sortKeys.value(p) : null,
            seq: seq++,
          });
        }
      }
    } finally {
      batch.release();
    }
  }

  // Phase 2: stable single-key sort. The seq tiebreaker pins equal keys to
  // their original input order, independent of when keys were materialized.
  if (plan.sort) {
    const compareKeys = makeKeyComparator(plan.sort.direction ?? 'asc');
    runs.sort((a, b) => compareKeys(a.sortKey, b.sortKey) || a.seq - b.seq);
  }

  // Phase 3: late materialization of projected columns only.
  const rows: ResultRow[] = runs.map((run) => ({
    rowId: run.ref.rowId,
    version: run.ref.version,
    values: {},
  }));
  const byBatch = new Map<number, Array<{ outPos: number; rowIndex: number }>>();
  runs.forEach((run, outPos) => {
    let list = byBatch.get(run.ref.batchId);
    if (!list) {
      list = [];
      byBatch.set(run.ref.batchId, list);
    }
    list.push({ outPos, rowIndex: run.ref.rowIndex });
  });
  for (const [batchId, list] of byBatch) {
    throwIfAborted(signal);
    const batch = await source.fetch(batchId);
    try {
      const indices = Uint32Array.from(list, (entry) => entry.rowIndex);
      for (const column of plan.project) {
        const vector = await loadValues(source, batch, column, indices, signal);
        for (let p = 0; p < list.length; p++) {
          rows[list[p].outPos].values[column] = vector.value(p);
        }
      }
    } finally {
      batch.release();
    }
  }

  return { rows, stats: source.stats.snapshot() };
}

async function loadValues(
  source: BatchSource,
  batch: RowBatch,
  column: string,
  indices: Uint32Array,
  signal?: AbortSignal,
): Promise<ColumnVector> {
  if (batch.hasColumn(column)) {
    const view = batch.column(column);
    const values: Value[] = new Array(indices.length);
    for (let p = 0; p < indices.length; p++) values[p] = view.value(indices[p]);
    return ColumnVector.fromValues(column, values);
  }
  return source.loadColumn(batch, column, indices, signal);
}
