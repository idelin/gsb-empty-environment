import { flattenVisible } from './flatten.ts';
import type {
  CellAddress,
  CellDisabledPredicate,
  ChildrenLoader,
  ColumnDef,
  TreeNode,
  VisibleGrid,
} from './types.ts';

export interface TreeGridOptions {
  nodes?: TreeNode[];
  columns?: ColumnDef[];
  /** Extra per-cell disable rule, combined with row/column flags. */
  isCellDisabled?: CellDisabledPredicate;
}

interface IndexEntry {
  node: TreeNode;
  parentId: string | null;
}

interface FocusState extends CellAddress {
  /** Last resolved column index; used as a hint when the column is deleted. */
  colIndex: number;
}

export class TreeGrid {
  private roots: TreeNode[];
  private columns: ColumnDef[];
  private index = new Map<string, IndexEntry>();
  private loading = new Map<string, Promise<void>>();
  private focused: FocusState | null = null;
  private readonly cellDisabledFn?: CellDisabledPredicate;

  constructor(options: TreeGridOptions = {}) {
    this.roots = options.nodes ?? [];
    this.columns = [...(options.columns ?? [])];
    this.cellDisabledFn = options.isCellDisabled;
    this.reindex();
  }

  // ---------------------------------------------------------------- model

  /** Flat visible grid: the single source of truth for navigation. */
  getVisibleGrid(): VisibleGrid {
    return { rows: flattenVisible(this.roots), columns: [...this.columns] };
  }

  getNode(rowId: string): TreeNode | undefined {
    return this.index.get(rowId)?.node;
  }

  isLoading(rowId: string): boolean {
    return this.loading.has(rowId);
  }

  isCellEnabled(node: TreeNode, column: ColumnDef): boolean {
    if (node.disabled || column.disabled) return false;
    return !(this.cellDisabledFn?.(node, column) ?? false);
  }

  // ---------------------------------------------------------------- focus

  getFocus(): CellAddress | null {
    this.resolveFocus();
    return this.focused ? { rowId: this.focused.rowId, colKey: this.focused.colKey } : null;
  }

  /**
   * Programmatic focus. Refuses hidden rows, unknown columns and
   * disabled cells: you can never jump into an unreachable cell.
   */
  focusCell(rowId: string, colKey: string): boolean {
    const grid = this.getVisibleGrid();
    const row = grid.rows.find((r) => r.id === rowId);
    const colIndex = grid.columns.findIndex((c) => c.key === colKey);
    if (!row || colIndex === -1) return false;
    if (!this.isCellEnabled(row.node, grid.columns[colIndex])) return false;
    this.focused = { rowId, colKey, colIndex };
    return true;
  }

  moveUp(): boolean {
    return this.moveVertical(-1);
  }

  moveDown(): boolean {
    return this.moveVertical(1);
  }

  moveLeft(): boolean {
    return this.moveHorizontal(-1);
  }

  moveRight(): boolean {
    return this.moveHorizontal(1);
  }

  private moveVertical(delta: -1 | 1): boolean {
    this.resolveFocus();
    const grid = this.getVisibleGrid();
    if (grid.rows.length === 0 || grid.columns.length === 0) return false;
    if (!this.focused) return this.focusFirstCell(grid);
    const rowIndex = grid.rows.findIndex((r) => r.id === this.focused!.rowId);
    const col = grid.columns[this.focused.colIndex];
    for (let r = rowIndex + delta; r >= 0 && r < grid.rows.length; r += delta) {
      if (this.isCellEnabled(grid.rows[r].node, col)) {
        this.focused = { rowId: grid.rows[r].id, colKey: col.key, colIndex: this.focused.colIndex };
        return true;
      }
    }
    return false;
  }

  private moveHorizontal(delta: -1 | 1): boolean {
    this.resolveFocus();
    const grid = this.getVisibleGrid();
    if (grid.rows.length === 0 || grid.columns.length === 0) return false;
    if (!this.focused) return this.focusFirstCell(grid);
    const row = grid.rows.find((r) => r.id === this.focused!.rowId)!;
    for (let c = this.focused.colIndex + delta; c >= 0 && c < grid.columns.length; c += delta) {
      if (this.isCellEnabled(row.node, grid.columns[c])) {
        this.focused = { rowId: row.id, colKey: grid.columns[c].key, colIndex: c };
        return true;
      }
    }
    return false;
  }

  private focusFirstCell(grid: VisibleGrid): boolean {
    for (const row of grid.rows) {
      for (let c = 0; c < grid.columns.length; c++) {
        if (this.isCellEnabled(row.node, grid.columns[c])) {
          this.focused = { rowId: row.id, colKey: grid.columns[c].key, colIndex: c };
          return true;
        }
      }
    }
    return false;
  }

  // ------------------------------------------------------------ expansion

  expand(rowId: string): boolean {
    const entry = this.index.get(rowId);
    if (!entry) return false;
    entry.node.expanded = true;
    this.resolveFocus();
    return true;
  }

  /**
   * Collapse a subtree. If the focus was inside it, focus returns to the
   * nearest visible ancestor row, keeping the column when possible.
   */
  collapse(rowId: string): boolean {
    const entry = this.index.get(rowId);
    if (!entry) return false;
    entry.node.expanded = false;
    this.resolveFocus();
    return true;
  }

  toggle(rowId: string): boolean {
    const entry = this.index.get(rowId);
    if (!entry) return false;
    return entry.node.expanded ? this.collapse(rowId) : this.expand(rowId);
  }

  /**
   * Async expansion for lazy trees: attaches loaded children, expands the
   * row, then re-resolves focus against the new flat grid. Concurrent
   * calls for the same row share one in-flight load.
   */
  async expandAsync(rowId: string, loader: ChildrenLoader): Promise<void> {
    const entry = this.index.get(rowId);
    if (!entry) throw new Error(`Unknown row: ${rowId}`);
    const pending = this.loading.get(rowId);
    if (pending) return pending;
    const task = (async () => {
      const children = await loader(entry.node);
      entry.node.children = children;
      entry.node.expanded = true;
      this.reindex();
      this.resolveFocus();
    })();
    this.loading.set(rowId, task);
    try {
      await task;
    } finally {
      this.loading.delete(rowId);
    }
  }

  // ------------------------------------------------------------- columns

  addColumn(column: ColumnDef, atIndex?: number): void {
    const at = atIndex === undefined ? this.columns.length : atIndex;
    this.columns.splice(at, 0, column);
    this.resolveFocus();
  }

  /**
   * Delete a column. Focus on the removed column falls back to the
   * nearest enabled column on the left (then right as a last resort).
   */
  removeColumn(colKey: string): boolean {
    const at = this.columns.findIndex((c) => c.key === colKey);
    if (at === -1) return false;
    this.columns.splice(at, 1);
    this.resolveFocus();
    return true;
  }

  // ------------------------------------------------------------ internals

  private reindex(): void {
    this.index.clear();
    const walk = (nodes: TreeNode[], parentId: string | null): void => {
      for (const node of nodes) {
        this.index.set(node.id, { node, parentId });
        if (node.children) walk(node.children, node.id);
      }
    };
    walk(this.roots, null);
  }

  /**
   * Reconcile focus with the current flat grid after any structural
   * change (collapse, async expand, column add/remove, disabled change).
   */
  private resolveFocus(): void {
    if (!this.focused) return;
    const grid = this.getVisibleGrid();

    let row = grid.rows.find((r) => r.id === this.focused!.rowId);
    if (!row) {
      const ancestorId = this.nearestVisibleAncestorId(this.focused.rowId, grid);
      row = ancestorId === null ? undefined : grid.rows.find((r) => r.id === ancestorId);
      if (!row) {
        this.focused = null;
        return;
      }
    }

    const colIndex = this.resolveColumnIndex(grid, row.node, this.focused);
    if (colIndex === -1) {
      this.focused = null;
      return;
    }
    this.focused = { rowId: row.id, colKey: grid.columns[colIndex].key, colIndex };
  }

  private nearestVisibleAncestorId(rowId: string, grid: VisibleGrid): string | null {
    const visible = new Set(grid.rows.map((r) => r.id));
    let entry = this.index.get(rowId);
    while (entry && entry.parentId !== null) {
      if (visible.has(entry.parentId)) return entry.parentId;
      entry = this.index.get(entry.parentId);
    }
    return null;
  }

  /**
   * Same column if it still exists and its cell is enabled; otherwise the
   * nearest enabled column to the left; as a last resort, to the right.
   */
  private resolveColumnIndex(grid: VisibleGrid, node: TreeNode, focus: FocusState): number {
    const cols = grid.columns;
    const exact = cols.findIndex((c) => c.key === focus.colKey);
    if (exact !== -1 && this.isCellEnabled(node, cols[exact])) return exact;

    const leftFrom = exact !== -1 ? exact - 1 : Math.min(focus.colIndex, cols.length) - 1;
    for (let c = leftFrom; c >= 0; c--) {
      if (this.isCellEnabled(node, cols[c])) return c;
    }
    const rightFrom = exact !== -1 ? exact + 1 : leftFrom + 1;
    for (let c = rightFrom; c < cols.length; c++) {
      if (this.isCellEnabled(node, cols[c])) return c;
    }
    return -1;
  }
}
