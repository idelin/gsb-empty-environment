/**
 * 列式存储引擎（带 IO 统计）。
 * 每个列独立存储；每次读取一个单元格计为 1 次 IO，
 * 用于验证延迟物化确实跳过了未胜出行的昂贵列读取。
 */
export type CellValue = number | string;

export class Column {
  readonly name: string;
  private readonly data: CellValue[];

  /** 该列被实际读取的单元格数（IO 计数器） */
  cellsRead = 0;

  constructor(name: string, data: CellValue[]) {
    this.name = name;
    this.data = data;
  }

  get size(): number {
    return this.data.length;
  }

  /** 按物理行号读取单个单元格（随机访问，计 1 次 IO） */
  read(rowId: number): CellValue {
    this.cellsRead++;
    return this.data[rowId];
  }

  /** 全列扫描（计 size 次 IO） */
  scanAll(): CellValue[] {
    this.cellsRead += this.data.length;
    return [...this.data];
  }

  /** 按行号集合批量读取（计 rowIds.length 次 IO） */
  readMany(rowIds: number[]): CellValue[] {
    this.cellsRead += rowIds.length;
    return rowIds.map((id) => this.data[id]);
  }
}

export class ColumnarTable {
  readonly numRows: number;
  private readonly columns = new Map<string, Column>();

  constructor(numRows: number) {
    this.numRows = numRows;
  }

  addColumn(column: Column): void {
    if (column.size !== this.numRows) {
      throw new Error(
        `Column "${column.name}" has ${column.size} rows, expected ${this.numRows}`,
      );
    }
    this.columns.set(column.name, column);
  }

  column(name: string): Column {
    const col = this.columns.get(name);
    if (!col) throw new Error(`Unknown column: ${name}`);
    return col;
  }

  get columnNames(): string[] {
    return [...this.columns.keys()];
  }

  /** 全 toJSON 之外的全表 IO 总量（所有列被读取的单元格数之和） */
  get totalCellsRead(): number {
    let total = 0;
    for (const col of this.columns.values()) total += col.cellsRead;
    return total;
  }

  resetIoCounters(): void {
    for (const col of this.columns.values()) col.cellsRead = 0;
  }
}
