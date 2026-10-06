export interface RowDef {
  id: string;
  parentId?: string | null;
  /** Column ids whose cell is disabled on this row. */
  disabledCells?: readonly string[];
  /** Initial expanded state (default false). */
  expanded?: boolean;
}

export interface CellPosition {
  rowId: string;
  columnId: string;
}

export type Direction = 'up' | 'down' | 'left' | 'right';
