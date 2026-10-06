import type { TreeTableModel } from './model.ts';

export interface GridCell {
  rowId: string;
  columnId: string;
  disabled: boolean;
}

export interface GridRow {
  id: string;
  depth: number;
  cells: GridCell[];
}

/** Flat, render-ready view of the tree table: only visible rows/columns. */
export interface VisibleGrid {
  columns: string[];
  rows: GridRow[];
}

export function buildVisibleGrid(model: TreeTableModel): VisibleGrid {
  const columns = model.getColumns();
  const rows = model.getVisibleRowsWithDepth().map(({ id, depth }) => ({
    id,
    depth,
    cells: columns.map((columnId) => ({
      rowId: id,
      columnId,
      disabled: model.isCellDisabled(id, columnId),
    })),
  }));
  return { columns, rows };
}

/**
 * Debug/test rendering: one string matrix, cells as "rowId:columnId",
 * suffixed with "!" when disabled.
 */
export function gridToMatrix(grid: VisibleGrid): string[][] {
  return grid.rows.map((row) =>
    grid.columns.map((columnId) => {
      const disabled = row.cells.find((cell) => cell.columnId === columnId)!.disabled;
      return `${row.id}:${columnId}${disabled ? '!' : ''}`;
    }),
  );
}
