import { SchemaGraph } from './schema.ts';

export type Row = Record<string, unknown>;

export class ForeignKeyViolationError extends Error {
  readonly code = 'FK_VIOLATION';

  constructor(message: string) {
    super(message);
    this.name = 'ForeignKeyViolationError';
  }
}

export class DuplicateKeyError extends Error {
  readonly code = 'DUPLICATE_KEY';

  constructor(message: string) {
    super(message);
    this.name = 'DuplicateKeyError';
  }
}

export class RowNotFoundError extends Error {
  readonly code = 'ROW_NOT_FOUND';

  constructor(message: string) {
    super(message);
    this.name = 'RowNotFoundError';
  }
}

export function encodeKey(values: unknown[]): string {
  return JSON.stringify(values);
}

export class TargetDatabase {
  private readonly graph: SchemaGraph;
  private readonly rows = new Map<string, Map<string, Row>>();

  constructor(graph: SchemaGraph) {
    this.graph = graph;
    for (const name of graph.tableNames()) {
      this.rows.set(name, new Map());
    }
  }

  keyOf(table: string, row: Row): string {
    const pk = this.graph.table(table).primaryKey;
    return encodeKey(pk.map((column) => row[column]));
  }

  has(table: string, key: string): boolean {
    return this.store(table).has(key);
  }

  get(table: string, key: string): Row | undefined {
    return this.store(table).get(key);
  }

  insert(table: string, row: Row): string {
    const key = this.keyOf(table, row);
    if (this.store(table).has(key)) {
      throw new DuplicateKeyError(`${table}${key} already exists`);
    }
    this.assertParentsExist(table, row);
    this.store(table).set(key, { ...row });
    return key;
  }

  update(table: string, row: Row): string {
    const key = this.keyOf(table, row);
    if (!this.store(table).has(key)) {
      throw new RowNotFoundError(`${table}${key} does not exist`);
    }
    this.assertParentsExist(table, row);
    this.store(table).set(key, { ...row });
    return key;
  }

  delete(table: string, key: string): void {
    if (!this.store(table).has(key)) {
      throw new RowNotFoundError(`${table}${key} does not exist`);
    }
    const referencing = this.childrenReferencing(table, key);
    if (referencing.length > 0) {
      const [first] = referencing;
      throw new ForeignKeyViolationError(
        `Cannot delete ${table}${key}: still referenced by ${first.table}${first.key}`,
      );
    }
    this.store(table).delete(key);
  }

  childrenReferencing(table: string, key: string): Array<{ table: string; key: string }> {
    const result: Array<{ table: string; key: string }> = [];
    for (const childName of this.graph.childrenOf(table)) {
      const childSchema = this.graph.table(childName);
      for (const fk of childSchema.foreignKeys) {
        if (fk.references.table !== table) {
          continue;
        }
        for (const [childKey, childRow] of this.store(childName)) {
          const parentKey = encodeKey(fk.columns.map((column) => childRow[column]));
          if (parentKey === key) {
            result.push({ table: childName, key: childKey });
          }
        }
      }
    }
    return result;
  }

  count(table: string): number {
    return this.store(table).size;
  }

  snapshot(): Record<string, Row[]> {
    const out: Record<string, Row[]> = {};
    for (const [name, rows] of this.rows) {
      out[name] = [...rows.values()];
    }
    return out;
  }

  private assertParentsExist(table: string, row: Row): void {
    for (const fk of this.graph.foreignKeysOf(table)) {
      const parentKey = encodeKey(fk.columns.map((column) => row[column]));
      if (!this.has(fk.references.table, parentKey)) {
        throw new ForeignKeyViolationError(
          `Cannot write ${table} row: missing parent ${fk.references.table}${parentKey} (fk: ${fk.name})`,
        );
      }
    }
  }

  private store(table: string): Map<string, Row> {
    const store = this.rows.get(table);
    if (!store) {
      throw new Error(`Unknown table: ${table}`);
    }
    return store;
  }
}
