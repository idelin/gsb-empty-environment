/**
 * Late Materialization projection operator.
 *
 * Pipeline (the whole point of the operator):
 *
 *   1. FILTER   — scan ONLY the filter column(s), build a selection vector
 *                 of physical row ids.
 *   2. SORT     — scan ONLY the sort column, restricted to the selection
 *                 vector, and order the surviving row ids.
 *   3. LIMIT    — truncate to the final winning row ids (top-k).
 *   4. MATERIALIZE — only now, point-fetch the remaining (expensive) columns
 *                 for the handful of winning row ids.
 *
 * For `SELECT * WHERE a > 10 ORDER BY b LIMIT 10` over a 100-column table,
 * steps 1-3 touch exactly 2 columns (a, b); step 4 touches the other 98
 * columns but only for 10 rows.
 */

import { ColumnStore } from './column-store.ts';
import type { CellValue, IOStats } from './column-store.ts';
import { compare } from './query.ts';
import type { QueryPlan } from './query.ts';

export type Row = Record<string, CellValue>;

export interface ExecutionResult {
  rows: Row[];
  /** Physical row ids of the winning rows, in final output order. */
  rowIds: number[];
  io: IOStats;
}

/** Columns the executor is allowed to scan up-front (cheap, selective). */
function earlyColumns(plan: QueryPlan): string[] {
  const cols: string[] = [];
  if (plan.filter) cols.push(plan.filter.column);
  if (plan.orderBy && plan.orderBy.column !== plan.filter?.column) {
    cols.push(plan.orderBy.column);
  }
  return cols;
}

export function executeLateMaterialization(
  store: ColumnStore,
  plan: QueryPlan,
): ExecutionResult {
  store.resetStats();

  // ---- Phase 1: FILTER — scan only the filter column, build selection vector.
  let selection: number[];
  if (plan.filter) {
    const { column, op, value } = plan.filter;
    const filterCol = store.scanColumn(column) as number[];
    selection = [];
    for (let i = 0; i < filterCol.length; i++) {
      if (compare(filterCol[i], op, value)) selection.push(i);
    }
  } else {
    selection = Array.from({ length: store.rowCount }, (_, i) => i);
  }

  // ---- Phase 2+3: ORDER BY ... LIMIT — scan only the sort column, keep top-k row ids.
  let winners: number[];
  if (plan.orderBy) {
    const { column, direction } = plan.orderBy;
    const sortCol = store.scanColumn(column);
    const sign = direction === 'desc' ? -1 : 1;
    const sorted = [...selection].sort((x, y) => {
      const vx = sortCol[x];
      const vy = sortCol[y];
      if (vx === vy) return x - y; // stable tie-break by physical row id
      return vx < vy ? -sign : sign;
    });
    winners = plan.limit !== undefined ? sorted.slice(0, plan.limit) : sorted;
  } else {
    winners = plan.limit !== undefined ? selection.slice(0, plan.limit) : selection;
  }

  // ---- Phase 4: LATE MATERIALIZATION — fetch every remaining column only for winners.
  const early = new Set(earlyColumns(plan));
  const materialized = new Map<string, CellValue[]>();
  for (const name of store.columnNames) {
    if (early.has(name)) continue; // already read; do not re-read
    materialized.set(name, store.readRows(name, winners));
  }

  // Assemble result rows: early columns are re-fetched positionally for the
  // winning ids only (no extra full scans).
  const rows: Row[] = winners.map(() => ({}));
  for (const name of store.columnNames) {
    const values = early.has(name)
      ? store.readRows(name, winners)
      : materialized.get(name)!;
    for (let r = 0; r < winners.length; r++) {
      rows[r][name] = values[r];
    }
  }

  return { rows, rowIds: winners, io: store.stats };
}

/**
 * Naive eager executor for comparison: scans ALL 100 columns up-front,
 * then filters/sorts/limits in memory. This is the IO baseline.
 */
export function executeEager(
  store: ColumnStore,
  plan: QueryPlan,
): ExecutionResult {
  store.resetStats();

  const data = new Map<string, CellValue[]>();
  for (const name of store.columnNames) {
    data.set(name, store.scanColumn(name)); // full scan of every column
  }

  let ids = Array.from({ length: store.rowCount }, (_, i) => i);
  if (plan.filter) {
    const { column, op, value } = plan.filter;
    const col = data.get(column) as number[];
    ids = ids.filter((i) => compare(col[i], op, value));
  }
  if (plan.orderBy) {
    const { column, direction } = plan.orderBy;
    const col = data.get(column)!;
    const sign = direction === 'desc' ? -1 : 1;
    ids.sort((x, y) => {
      const vx = col[x];
      const vy = col[y];
      if (vx === vy) return x - y;
      return vx < vy ? -sign : sign;
    });
  }
  if (plan.limit !== undefined) ids = ids.slice(0, plan.limit);

  const rows: Row[] = ids.map((id) => {
    const row: Row = {};
    for (const name of store.columnNames) row[name] = data.get(name)![id];
    return row;
  });

  return { rows, rowIds: ids, io: store.stats };
}
