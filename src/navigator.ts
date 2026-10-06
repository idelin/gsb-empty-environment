import type { TreeTableModel } from './model.ts';
import type { CellPosition, Direction } from './types.ts';

/**
 * Cell focus navigation over the flat visible grid of a TreeTableModel.
 *
 * Rules:
 * - Moves only ever land on visible, enabled cells; disabled cells are
 *   skipped and hidden rows can never be entered.
 * - After any model mutation (collapse, row/column removal, async expand),
 *   focus is reconciled: a hidden or removed focused row falls back to its
 *   nearest visible ancestor in the same column; a missing column falls
 *   back to the nearest left column (then nearest right as a last resort).
 */
export class Navigator {
  private model: TreeTableModel;
  private focusCell: CellPosition | null = null;
  /** Ancestor ids of the focused row, nearest first, from last placement. */
  private focusRowChain: string[] = [];
  private lastColumns: string[];
  private unsubscribe: () => void;

  constructor(model: TreeTableModel) {
    this.model = model;
    this.lastColumns = model.getColumns();
    this.unsubscribe = model.onChange(() => this.reconcile());
  }

  dispose(): void {
    this.unsubscribe();
  }

  get focus(): CellPosition | null {
    return this.focusCell ? { ...this.focusCell } : null;
  }

  /** Focus a cell. Refuses hidden rows, unknown columns, disabled cells. */
  setFocus(rowId: string, columnId: string): boolean {
    if (!this.model.isVisible(rowId)) return false;
    if (!this.model.hasColumn(columnId)) return false;
    if (this.model.isCellDisabled(rowId, columnId)) return false;
    this.focusCell = { rowId, columnId };
    this.refreshChain();
    return true;
  }

  /** Focus the first enabled cell of the visible grid. */
  focusFirst(): boolean {
    const first = this.firstEnabledCell();
    if (!first) return false;
    this.focusCell = first;
    this.refreshChain();
    return true;
  }

  move(direction: Direction): boolean {
    switch (direction) {
      case 'up': return this.moveVertical(-1);
      case 'down': return this.moveVertical(1);
      case 'left': return this.moveHorizontal(-1);
      case 'right': return this.moveHorizontal(1);
    }
  }

  moveUp(): boolean { return this.move('up'); }
  moveDown(): boolean { return this.move('down'); }
  moveLeft(): boolean { return this.move('left'); }
  moveRight(): boolean { return this.move('right'); }

  /** Same column, next visible row whose cell is enabled. */
  private moveVertical(step: 1 | -1): boolean {
    if (!this.focusCell) return false;
    const rows = this.model.getVisibleRows();
    const index = rows.indexOf(this.focusCell.rowId);
    if (index === -1) return false;
    const { columnId } = this.focusCell;
    for (let i = index + step; i >= 0 && i < rows.length; i += step) {
      if (!this.model.isCellDisabled(rows[i], columnId)) {
        this.focusCell = { rowId: rows[i], columnId };
        this.refreshChain();
        return true;
      }
    }
    return false;
  }

  /** Same row, next column whose cell is enabled. */
  private moveHorizontal(step: 1 | -1): boolean {
    if (!this.focusCell) return false;
    const columns = this.model.getColumns();
    const index = columns.indexOf(this.focusCell.columnId);
    if (index === -1) return false;
    const { rowId } = this.focusCell;
    for (let i = index + step; i >= 0 && i < columns.length; i += step) {
      if (!this.model.isCellDisabled(rowId, columns[i])) {
        this.focusCell = { rowId, columnId: columns[i] };
        return true;
      }
    }
    return false;
  }

  // ---- reconciliation after model mutations ----

  private reconcile(): void {
    const prevColumns = this.lastColumns;
    this.lastColumns = this.model.getColumns();
    if (!this.focusCell) return;

    const desiredColumn = this.focusCell.columnId;
    let rowId: string | null = this.focusCell.rowId;
    if (!this.model.isVisible(rowId)) {
      // Row hidden by a collapse, or removed entirely: consult the
      // remembered ancestor chain (the row may no longer exist).
      rowId = this.focusRowChain.find((id) => this.model.isVisible(id)) ?? null;
    }
    while (rowId !== null) {
      const columnId = this.resolveColumn(rowId, desiredColumn, prevColumns);
      if (columnId !== null) {
        this.focusCell = { rowId, columnId };
        this.refreshChain();
        return;
      }
      rowId = this.nearestVisibleAncestor(rowId);
    }
    this.focusCell = this.firstEnabledCell();
    if (this.focusCell) this.refreshChain();
  }

  private refreshChain(): void {
    const chain: string[] = [];
    let parentId = this.model.getParentId(this.focusCell!.rowId);
    while (parentId !== null) {
      chain.push(parentId);
      parentId = this.model.getParentId(parentId);
    }
    this.focusRowChain = chain;
  }

  private nearestVisibleAncestor(rowId: string): string | null {
    let parentId = this.model.getParentId(rowId);
    while (parentId !== null && !this.model.isVisible(parentId)) {
      parentId = this.model.getParentId(parentId);
    }
    return parentId;
  }

  /**
   * Pick a column on `rowId`: the desired column when present and enabled,
   * otherwise the nearest enabled column to its left, then to its right.
   * `prevColumns` (the column order before the mutation) locates the gap
   * left by a deleted column.
   */
  private resolveColumn(
    rowId: string,
    desired: string,
    prevColumns: string[],
  ): string | null {
    const columns = this.model.getColumns();
    if (columns.length === 0) return null;
    const enabled = (columnId: string): boolean =>
      !this.model.isCellDisabled(rowId, columnId);
    if (columns.includes(desired) && enabled(desired)) return desired;

    let left: string[];
    let right: string[];
    if (columns.includes(desired)) {
      const index = columns.indexOf(desired);
      left = columns.slice(0, index).reverse();
      right = columns.slice(index + 1);
    } else {
      const prevIndex = prevColumns.indexOf(desired);
      const before = prevIndex === -1 ? prevColumns : prevColumns.slice(0, prevIndex);
      const after = prevIndex === -1 ? [] : prevColumns.slice(prevIndex + 1);
      left = before.reverse().filter((col) => columns.includes(col));
      right = after.filter((col) => columns.includes(col));
    }
    for (const col of left) if (enabled(col)) return col;
    for (const col of right) if (enabled(col)) return col;
    return null;
  }

  private firstEnabledCell(): CellPosition | null {
    const columns = this.model.getColumns();
    for (const rowId of this.model.getVisibleRows()) {
      for (const columnId of columns) {
        if (!this.model.isCellDisabled(rowId, columnId)) {
          return { rowId, columnId };
        }
      }
    }
    return null;
  }
}
