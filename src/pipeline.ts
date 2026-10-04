import { ColumnBatch, ColumnView, SelectionVector } from './batch';
import { applyOp, compareSortKeys } from './compare';
import { BatchSource } from './source';
import { Stats } from './stats';
import { Plan, Predicate, QueryResult, Value } from './types';

/**
 * Narrow-column filtering. Produces a selection vector bound to the
 * batch; a row is kept only when the predicate is exactly `true`
 * (null operands make it unknown, and unknown rows are dropped).
 */
export function filterBatch(batch: ColumnBatch, pred: Predicate): SelectionVector {
  const view = batch.narrowView(pred.col);
  const indices = new Uint32Array(batch.rowCount);
  let count = 0;
  for (let i = 0; i < batch.rowCount; i++) {
    if (applyOp(pred.op, view.valueAt(i), pred.value) === true) {
      indices[count++] = i;
    }
  }
  return new SelectionVector(batch, indices, count);
}

export function selectAll(batch: ColumnBatch): SelectionVector {
  const indices = new Uint32Array(batch.rowCount);
  for (let i = 0; i < batch.rowCount; i++) indices[i] = i;
  return new SelectionVector(batch, indices, batch.rowCount);
}

interface SelectedRow {
  batch: ColumnBatch;
  row: number;
  seq: number;
  key: Value;
}

/**
 * Late-materializing execution of filter -> sort -> project:
 *
 * 1. Scan: evaluate the filter on narrow columns only, producing a
 *    selection vector per batch. If sorting, the key column is read
 *    now (narrow directly, wide via one cancellable async load) and
 *    each selected row is stamped with a global arrival sequence.
 * 2. Sort: single-key, stable — ties (including null keys) fall back
 *    to the arrival sequence, so the output order is fixed before any
 *    projected wide column is loaded and can never be disturbed by
 *    delayed reads.
 * 3. Project: only now are projected wide columns loaded (async,
 *    cancellable, counted). Columns that are not projected are never
 *    read; batches with an empty selection load nothing.
 *
 * Batches are pinned for the duration of the query so their slots
 * cannot be recycled underneath live views.
 */
export async function execute(
  source: BatchSource,
  plan: Plan,
  opts?: { signal?: AbortSignal },
): Promise<QueryResult> {
  const stats: Stats = source.stats;
  const pinned: ColumnBatch[] = [];
  const selected: SelectedRow[] = [];
  const usedBatches: ColumnBatch[] = [];
  let seq = 0;
  try {
    for await (const batch of source.batches()) {
      batch.pin();
      pinned.push(batch);
      const sel = plan.filter ? filterBatch(batch, plan.filter) : selectAll(batch);
      stats.rowsScanned += batch.rowCount;
      stats.rowsSelected += sel.count;
      if (sel.count === 0) continue;
      usedBatches.push(batch);

      let keyView: ColumnView | null = null;
      if (plan.sort) keyView = await columnView(batch, plan.sort.key, opts);
      const rows = sel.resolve();
      for (let i = 0; i < sel.count; i++) {
        const row = rows[i];
        selected.push({
          batch,
          row,
          seq: seq++,
          key: keyView ? keyView.valueAt(row) : null,
        });
      }
    }

    if (plan.sort) {
      const dir = plan.sort.dir === 'desc' ? -1 : 1;
      selected.sort((x, y) => {
        const c = compareSortKeys(x.key, y.key);
        if (c !== 0) return c * dir;
        return x.seq - y.seq;
      });
    }

    const views = new Map<ColumnBatch, Map<string, ColumnView>>();
    for (const batch of usedBatches) {
      const perBatch = new Map<string, ColumnView>();
      for (const col of plan.project) {
        perBatch.set(col, await columnView(batch, col, opts));
      }
      views.set(batch, perBatch);
    }

    const rows = selected.map((s) => {
      const perBatch = views.get(s.batch)!;
      return {
        rowId: s.batch.rowIds[s.row],
        rowVersion: s.batch.rowVersions[s.row],
        values: plan.project.map((col) => perBatch.get(col)!.valueAt(s.row)),
      };
    });
    stats.rowsEmitted += rows.length;
    return { columns: plan.project.slice(), rows };
  } finally {
    for (const b of pinned) b.unpin();
  }
}

async function columnView(
  batch: ColumnBatch,
  name: string,
  opts?: { signal?: AbortSignal },
): Promise<ColumnView> {
  if (batch.isWide(name)) return batch.loadWide(name, opts);
  return batch.narrowView(name);
}
