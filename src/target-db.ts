import { DependencyGraph } from "./graph.ts";
import type { Row } from "./types.ts";

/** Raised when a write would violate a foreign-key constraint. */
export class ForeignKeyViolationError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ForeignKeyViolationError";
  }
}

/**
 * In-memory stand-in for the target database.
 * Enforces FK constraints exactly like a real target would:
 * - INSERT/UPDATE: every non-null FK parent row must exist.
 * - DELETE: no child row may still reference the deleted row.
 */
export class InMemoryTargetDb {
  private graph: DependencyGraph;
  private tables = new Map<string, Map<string, Row>>();
  /** Event ids in the order they were committed — used for assertions. */
  readonly commitLog: string[] = [];

  constructor(graph: DependencyGraph) {
    this.graph = graph;
    for (const name of graph.tableNames()) this.tables.set(name, new Map());
  }

  private bucket(table: string): Map<string, Row> {
    const b = this.tables.get(table);
    if (!b) throw new Error(`unknown table: ${table}`);
    return b;
  }

  private pkOf(table: string, row: Row): string {
    const pk = this.graph.tableDef(table).primaryKey;
    const v = row[pk];
    if (v === undefined || v === null) {
      throw new Error(`row in ${table} is missing primary key ${pk}`);
    }
    return String(v);
  }

  has(table: string, pkValue: unknown): boolean {
    return this.bucket(table).has(String(pkValue));
  }

  get(table: string, pkValue: unknown): Row | undefined {
    return this.bucket(table).get(String(pkValue));
  }

  rowCount(table: string): number {
    return this.bucket(table).size;
  }

  /** True when every non-null FK of `row` points at an existing parent row. */
  parentsExist(table: string, row: Row): boolean {
    return this.missingParents(table, row).length === 0;
  }

  /** Names of FK constraints whose parent row is currently absent. */
  missingParents(table: string, row: Row): string[] {
    const missing: string[] = [];
    for (const fk of this.graph.foreignKeysOf(table)) {
      const v = row[fk.childColumn];
      if (v === null || v === undefined) continue;
      if (!this.has(fk.parentTable, v)) missing.push(fk.name);
    }
    return missing;
  }

  /** True when no existing child row references `table(pkValue)`. */
  noChildrenReference(table: string, pkValue: unknown): boolean {
    return this.blockingChildren(table, pkValue).length === 0;
  }

  /** Names of FK constraints under which child rows still reference this row. */
  blockingChildren(table: string, pkValue: unknown): string[] {
    const blocking: string[] = [];
    for (const fk of this.graph.childKeysOf(table)) {
      for (const childRow of this.bucket(fk.childTable).values()) {
        if (childRow[fk.childColumn] === pkValue) {
          blocking.push(fk.name);
          break;
        }
      }
    }
    return blocking;
  }

  insert(table: string, row: Row, eventId?: string): void {
    const key = this.pkOf(table, row);
    if (this.bucket(table).has(key)) {
      throw new Error(`duplicate primary key ${table}(${key})`);
    }
    const missing = this.missingParents(table, row);
    if (missing.length > 0) {
      throw new ForeignKeyViolationError(
        `INSERT into ${table}(${key}) violates FK: ${missing.join(", ")}`,
      );
    }
    this.bucket(table).set(key, { ...row });
    if (eventId !== undefined) this.commitLog.push(eventId);
  }

  update(table: string, row: Row, eventId?: string): void {
    const key = this.pkOf(table, row);
    if (!this.bucket(table).has(key)) {
      throw new Error(`UPDATE of missing row ${table}(${key})`);
    }
    const missing = this.missingParents(table, row);
    if (missing.length > 0) {
      throw new ForeignKeyViolationError(
        `UPDATE of ${table}(${key}) violates FK: ${missing.join(", ")}`,
      );
    }
    this.bucket(table).set(key, { ...row });
    if (eventId !== undefined) this.commitLog.push(eventId);
  }

  delete(table: string, pkValue: unknown, eventId?: string): void {
    const key = String(pkValue);
    if (!this.bucket(table).has(key)) {
      throw new Error(`DELETE of missing row ${table}(${key})`);
    }
    const blocking = this.blockingChildren(table, pkValue);
    if (blocking.length > 0) {
      throw new ForeignKeyViolationError(
        `DELETE of ${table}(${key}) violates FK: ${blocking.join(", ")}`,
      );
    }
    this.bucket(table).delete(key);
    if (eventId !== undefined) this.commitLog.push(eventId);
  }

  snapshot(): Record<string, Row[]> {
    const out: Record<string, Row[]> = {};
    for (const [name, rows] of this.tables) out[name] = [...rows.values()];
    return out;
  }
}
