import { flattenJson, inferPaths, type JsonValue } from "./flatten.ts";
import { extractPathsFromRaw, type ExtractStats } from "./selective.ts";

export interface QueryResult {
  /** Requested path -> column slice (one slot per ingested row). */
  columns: Record<string, (JsonValue | undefined)[]>;
  rowCount: number;
}

/**
 * Columnar projector for semi-structured JSON documents.
 *
 * Each distinct leaf path becomes an independent virtual column. Rows can
 * be ingested either from already-parsed values (full flatten) or directly
 * from raw JSON text with an explicit path list, in which case only those
 * paths are materialized and the rest of the document is never
 * deserialized.
 */
export class ColumnarProjector {
  private readonly columns = new Map<string, (JsonValue | undefined)[]>();
  private rowCount = 0;
  private lastStats: ExtractStats | null = null;

  /** Infers the leaf paths (virtual column names) of a sample document. */
  inferPaths(sample: JsonValue): string[] {
    return inferPaths(sample);
  }

  /** Names of all virtual columns known so far, sorted. */
  columnNames(): string[] {
    return [...this.columns.keys()].sort();
  }

  /** Number of ingested rows. */
  size(): number {
    return this.rowCount;
  }

  /** Stats of the most recent selective (raw) ingestion. */
  getLastExtractStats(): ExtractStats | null {
    return this.lastStats;
  }

  /** Ingests a parsed document, flattening every leaf into its column. */
  addRow(row: JsonValue): void {
    const flat = flattenJson(row);
    for (const [path, value] of flat) {
      this.column(path)[this.rowCount] = value;
    }
    for (const column of this.columns.values()) {
      if (column.length < this.rowCount + 1) {
        column[this.rowCount] = undefined;
      }
    }
    this.rowCount += 1;
  }

  /**
   * Ingests a raw JSON string, materializing only `paths`. Every other
   * subtree of the document is skipped by a raw scan and never parsed
   * into objects/arrays/scalars.
   */
  addRowSelective(raw: string, paths: readonly string[]): ExtractStats {
    const { values, stats } = extractPathsFromRaw(raw, paths);
    for (const path of paths) {
      this.column(path)[this.rowCount] = values.get(path);
    }
    for (const column of this.columns.values()) {
      if (column.length < this.rowCount + 1) {
        column[this.rowCount] = undefined;
      }
    }
    this.rowCount += 1;
    this.lastStats = stats;
    return stats;
  }

  /**
   * Reads only the requested virtual columns. Untouched columns are not
   * accessed, mirroring a columnar scan with projection pushdown.
   */
  query(paths: readonly string[]): QueryResult {
    const columns: Record<string, (JsonValue | undefined)[]> = {};
    for (const path of paths) {
      const column = this.columns.get(path);
      const slice: (JsonValue | undefined)[] = new Array(this.rowCount);
      for (let i = 0; i < this.rowCount; i += 1) {
        slice[i] = column ? column[i] : undefined;
      }
      columns[path] = slice;
    }
    return { columns, rowCount: this.rowCount };
  }

  /** Returns one virtual column by path (undefined-filled if unknown). */
  column(path: string): (JsonValue | undefined)[] {
    let column = this.columns.get(path);
    if (!column) {
      column = new Array(this.rowCount).fill(undefined);
      this.columns.set(path, column);
    }
    return column;
  }
}
