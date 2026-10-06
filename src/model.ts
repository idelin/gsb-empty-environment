import type { RowDef } from './types.ts';

interface RowState {
  id: string;
  parentId: string | null;
  childIds: string[];
  expanded: boolean;
  loading: boolean;
  disabledCells: Set<string>;
}

export type RowLoader = () => Promise<readonly RowDef[]>;

/**
 * Tree-table data model. Rows form a forest; columns are an ordered list.
 * Emits a change event after every mutation so observers (e.g. Navigator)
 * can reconcile against the new flat visible grid.
 */
export class TreeTableModel {
  private rows = new Map<string, RowState>();
  private rootIds: string[] = [];
  private columnIds: string[] = [];
  private listeners = new Set<() => void>();

  constructor(columns: readonly string[] = [], rows: readonly RowDef[] = []) {
    for (const col of columns) this.addColumn(col);
    for (const row of rows) this.addRow(row);
  }

  onChange(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  private emit(): void {
    for (const listener of [...this.listeners]) listener();
  }

  // ---- columns ----

  addColumn(id: string, index?: number): void {
    if (this.columnIds.includes(id)) throw new Error(`duplicate column: ${id}`);
    if (index === undefined) this.columnIds.push(id);
    else this.columnIds.splice(index, 0, id);
    this.emit();
  }

  removeColumn(id: string): void {
    const index = this.columnIds.indexOf(id);
    if (index === -1) return;
    this.columnIds.splice(index, 1);
    this.emit();
  }

  getColumns(): string[] {
    return [...this.columnIds];
  }

  hasColumn(id: string): boolean {
    return this.columnIds.includes(id);
  }

  // ---- rows ----

  addRow(def: RowDef): void {
    if (this.rows.has(def.id)) throw new Error(`duplicate row: ${def.id}`);
    const parentId = def.parentId ?? null;
    let parent: RowState | undefined;
    if (parentId !== null) {
      parent = this.rows.get(parentId);
      if (!parent) throw new Error(`unknown parent row: ${parentId}`);
    }
    this.rows.set(def.id, {
      id: def.id,
      parentId,
      childIds: [],
      expanded: def.expanded ?? false,
      loading: false,
      disabledCells: new Set(def.disabledCells ?? []),
    });
    if (parent) parent.childIds.push(def.id);
    else this.rootIds.push(def.id);
    this.emit();
  }

  /** Removes a row and its whole subtree. */
  removeRow(id: string): void {
    const row = this.rows.get(id);
    if (!row) return;
    const detachFrom = row.parentId === null
      ? this.rootIds
      : this.rows.get(row.parentId)!.childIds;
    detachFrom.splice(detachFrom.indexOf(id), 1);
    const stack = [id];
    while (stack.length > 0) {
      const current = this.rows.get(stack.pop()!)!;
      stack.push(...current.childIds);
      this.rows.delete(current.id);
    }
    this.emit();
  }

  hasRow(id: string): boolean {
    return this.rows.has(id);
  }

  getParentId(id: string): string | null {
    return this.rows.get(id)?.parentId ?? null;
  }

  getChildIds(id: string): string[] {
    const row = this.rows.get(id);
    return row ? [...row.childIds] : [];
  }

  // ---- expansion ----

  isExpanded(id: string): boolean {
    return this.rows.get(id)?.expanded ?? false;
  }

  isLoading(id: string): boolean {
    return this.rows.get(id)?.loading ?? false;
  }

  expand(id: string): void {
    const row = this.rows.get(id);
    if (!row || row.expanded) return;
    row.expanded = true;
    this.emit();
  }

  collapse(id: string): void {
    const row = this.rows.get(id);
    if (!row || !row.expanded) return;
    row.expanded = false;
    this.emit();
  }

  toggle(id: string): void {
    if (this.isExpanded(id)) this.collapse(id);
    else this.expand(id);
  }

  /**
   * Async expand: marks the row loading, awaits the loader, appends the
   * loaded children, then expands. The flat visible grid only changes once
   * the loader resolves. Rejects (and clears loading) if the loader fails.
   */
  async expandAsync(id: string, loader: RowLoader): Promise<void> {
    const row = this.rows.get(id);
    if (!row) throw new Error(`unknown row: ${id}`);
    if (row.expanded) return;
    if (row.loading) throw new Error(`row already loading: ${id}`);
    row.loading = true;
    this.emit();
    let children: readonly RowDef[];
    try {
      children = await loader();
    } catch (error) {
      row.loading = false;
      this.emit();
      throw error;
    }
    row.loading = false;
    for (const child of children) {
      this.addRow({ ...child, parentId: id });
    }
    row.expanded = true;
    this.emit();
  }

  // ---- cells ----

  isCellDisabled(rowId: string, columnId: string): boolean {
    return this.rows.get(rowId)?.disabledCells.has(columnId) ?? false;
  }

  setCellDisabled(rowId: string, columnId: string, disabled: boolean): void {
    const row = this.rows.get(rowId);
    if (!row) throw new Error(`unknown row: ${rowId}`);
    if (disabled) row.disabledCells.add(columnId);
    else row.disabledCells.delete(columnId);
    this.emit();
  }

  // ---- flat visible grid ----

  /** A row is visible when it exists and every ancestor is expanded. */
  isVisible(rowId: string): boolean {
    const row = this.rows.get(rowId);
    if (!row) return false;
    let parentId = row.parentId;
    while (parentId !== null) {
      const parent = this.rows.get(parentId);
      if (!parent || !parent.expanded) return false;
      parentId = parent.parentId;
    }
    return true;
  }

  /** Visible rows in depth-first order, with indentation depth. */
  getVisibleRowsWithDepth(): Array<{ id: string; depth: number }> {
    const out: Array<{ id: string; depth: number }> = [];
    const walk = (ids: string[], depth: number): void => {
      for (const id of ids) {
        out.push({ id, depth });
        const row = this.rows.get(id)!;
        if (row.expanded) walk(row.childIds, depth + 1);
      }
    };
    walk(this.rootIds, 0);
    return out;
  }

  getVisibleRows(): string[] {
    return this.getVisibleRowsWithDepth().map((entry) => entry.id);
  }
}
