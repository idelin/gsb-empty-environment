/**
 * Columnar storage engine with IO accounting.
 *
 * Every read goes through one of two access paths, mirroring a real
 * columnar engine (e.g. Parquet/Arrow scans vs. row-group point lookups):
 *
 *  - scanColumn(name)      : full column scan. Cost = rowCount cells.
 *  - readRows(name, ids)   : positional point lookups. Cost = ids.length cells.
 *
 * The IO stats let tests and the demo prove that the late-materialization
 * executor never touches the 98 expensive columns until the final
 * top-k row ids are known.
 */

export type CellValue = number | string;

export interface IOStats {
  /** Number of full column scans (each costs `rowCount` cell reads). */
  fullColumnScans: number;
  /** Names of columns that were fully scanned, in scan order. */
  scannedColumns: string[];
  /** Number of positional point lookups (late-materialized fetches). */
  pointLookups: number;
  /** Total cells read = fullColumnScans * rowCount + pointLookups. */
  cellsRead: number;
}

export class ColumnStore {
  readonly rowCount: number;
  private readonly columns = new Map<string, CellValue[]>();
  private readonly columnOrder: string[] = [];

  private fullColumnScans = 0;
  private scannedColumns: string[] = [];
  private pointLookups = 0;

  constructor(rowCount: number) {
    if (!Number.isInteger(rowCount) || rowCount < 0) {
      throw new Error(`rowCount must be a non-negative integer, got ${rowCount}`);
    }
    this.rowCount = rowCount;
  }

  addColumn(name: string, values: CellValue[]): void {
    if (values.length !== this.rowCount) {
      throw new Error(
        `column "${name}" has ${values.length} values, expected ${this.rowCount}`,
      );
    }
    if (this.columns.has(name)) {
      throw new Error(`duplicate column "${name}"`);
    }
    this.columns.set(name, values);
    this.columnOrder.push(name);
  }

  get columnNames(): string[] {
    return [...this.columnOrder];
  }

  hasColumn(name: string): boolean {
    return this.columns.has(name);
  }

  /** Full column scan: the expensive access path the optimizer tries to avoid. */
  scanColumn(name: string): CellValue[] {
    const col = this.mustGet(name);
    this.fullColumnScans += 1;
    this.scannedColumns.push(name);
    return col.slice();
  }

  /** Positional fetch of specific physical row ids: the cheap late-materialization path. */
  readRows(name: string, rowIds: number[]): CellValue[] {
    const col = this.mustGet(name);
    this.pointLookups += rowIds.length;
    return rowIds.map((id) => {
      if (id < 0 || id >= this.rowCount) {
        throw new Error(`row id ${id} out of range [0, ${this.rowCount})`);
      }
      return col[id];
    });
  }

  get stats(): IOStats {
    return {
      fullColumnScans: this.fullColumnScans,
      scannedColumns: [...this.scannedColumns],
      pointLookups: this.pointLookups,
      cellsRead: this.fullColumnScans * this.rowCount + this.pointLookups,
    };
  }

  resetStats(): void {
    this.fullColumnScans = 0;
    this.scannedColumns = [];
    this.pointLookups = 0;
  }

  private mustGet(name: string): CellValue[] {
    const col = this.columns.get(name);
    if (!col) throw new Error(`unknown column "${name}"`);
    return col;
  }
}
