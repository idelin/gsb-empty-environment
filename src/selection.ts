// Map a selection rectangle from an old snapshot onto a newer one.
// Identity is by column id, never by position: columns keep meaning across
// hide/reorder/collapse. Result is a candidate list, or invalidation when
// nothing the selection covered is visible any more.
import type { HeaderCell, HeaderSnapshot } from './types.ts';

export interface CellPoint {
  row: number;
  col: number;
}

export interface Selection {
  anchor: CellPoint;
  focus: CellPoint;
}

export interface SelectionCandidate {
  cellId: string;
  row: number;
  col: number;
  rowSpan: number;
  colSpan: number;
  columnIds: string[];
}

export type SelectionMapResult =
  | { status: 'exact'; candidates: SelectionCandidate[] }
  | { status: 'partial'; candidates: SelectionCandidate[]; lostColumnIds: string[] }
  | { status: 'invalidated'; reason: string };

function normalize(sel: Selection): { r0: number; c0: number; r1: number; c1: number } {
  return {
    r0: Math.min(sel.anchor.row, sel.focus.row),
    c0: Math.min(sel.anchor.col, sel.focus.col),
    r1: Math.max(sel.anchor.row, sel.focus.row),
    c1: Math.max(sel.anchor.col, sel.focus.col),
  };
}

function intersects(cell: HeaderCell, rect: { r0: number; c0: number; r1: number; c1: number }): boolean {
  return (
    cell.row <= rect.r1 &&
    cell.row + cell.rowSpan - 1 >= rect.r0 &&
    cell.col <= rect.c1 &&
    cell.col + cell.colSpan - 1 >= rect.c0
  );
}

function toCandidate(cell: HeaderCell): SelectionCandidate {
  return {
    cellId: cell.id,
    row: cell.row,
    col: cell.col,
    rowSpan: cell.rowSpan,
    colSpan: cell.colSpan,
    columnIds: [...cell.coveredColumnIds],
  };
}

export function mapSelection(
  from: HeaderSnapshot,
  to: HeaderSnapshot,
  selection: Selection,
): SelectionMapResult {
  const rect = normalize(selection);
  if (rect.r1 >= from.rows || rect.c1 >= from.columnCount || rect.r0 < 0 || rect.c0 < 0) {
    return { status: 'invalidated', reason: 'selection outside source header grid' };
  }

  const coveredIds = new Set<string>();
  for (const cell of from.cells) {
    if (intersects(cell, rect)) {
      for (const id of cell.coveredColumnIds) coveredIds.add(id);
    }
  }
  if (coveredIds.size === 0) {
    return { status: 'invalidated', reason: 'selection covers no columns' };
  }

  const cellByColumnId = new Map<string, HeaderCell>();
  for (const cell of to.cells) {
    for (const id of cell.coveredColumnIds) {
      if (!cellByColumnId.has(id)) cellByColumnId.set(id, cell);
    }
  }

  const candidates = new Map<string, SelectionCandidate>();
  const lostColumnIds: string[] = [];
  for (const id of coveredIds) {
    const cell = cellByColumnId.get(id);
    if (cell) candidates.set(cell.id, toCandidate(cell));
    else lostColumnIds.push(id);
  }

  if (candidates.size === 0) {
    return {
      status: 'invalidated',
      reason: 'no selected column is visible in the target header',
    };
  }
  if (lostColumnIds.length > 0) {
    return {
      status: 'partial',
      candidates: [...candidates.values()],
      lostColumnIds: lostColumnIds.sort(),
    };
  }
  return { status: 'exact', candidates: [...candidates.values()] };
}
