/**
 * 列式存储：每个字段路径对应一个虚拟列，列单元按行号对齐。
 *
 * 单元折叠规则（getColumn）：
 *   0 个值  -> undefined（该记录缺失此路径）
 *   1 个值  -> 值本身
 *   多个值  -> 值数组（数组通配路径在同一记录内命中多次时出现）
 */
export class ColumnStore {
  rowCount = 0;

  private readonly cols = new Map<string, unknown[][]>();

  addValue(path: string, row: number, value: unknown): void {
    let col = this.cols.get(path);
    if (!col) {
      col = [];
      this.cols.set(path, col);
    }
    while (col.length <= row) col.push([]);
    col[row].push(value);
  }

  has(path: string): boolean {
    return this.cols.has(path);
  }

  get columnCount(): number {
    return this.cols.size;
  }

  /** 全部已推断/已物化的列路径（字典序）。 */
  paths(): string[] {
    return [...this.cols.keys()].sort();
  }

  /** 折叠后的列：长度等于 rowCount，缺失行为 undefined。 */
  getColumn(path: string): unknown[] {
    const col = this.cols.get(path);
    const out: unknown[] = new Array(this.rowCount).fill(undefined);
    if (!col) return out;
    for (let row = 0; row < col.length && row < this.rowCount; row++) {
      const cell = col[row];
      if (cell.length === 1) out[row] = cell[0];
      else if (cell.length > 1) out[row] = cell.slice();
    }
    return out;
  }

  /** 未折叠的列：每个单元是该行命中值的列表。 */
  getRawColumn(path: string): unknown[][] {
    return this.cols.get(path) ?? [];
  }

  toObject(): Record<string, unknown[]> {
    const out: Record<string, unknown[]> = {};
    for (const path of this.paths()) out[path] = this.getColumn(path);
    return out;
  }
}
