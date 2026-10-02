export interface ForeignKey {
  name: string;
  columns: string[];
  references: {
    table: string;
    columns: string[];
  };
}

export interface TableSchema {
  name: string;
  primaryKey: string[];
  foreignKeys: ForeignKey[];
}

export type SchemaDefinition = TableSchema[];

export class SchemaCycleError extends Error {
  readonly tables: string[];

  constructor(tables: string[]) {
    super(`Foreign-key dependency cycle detected among tables: ${tables.join(', ')}`);
    this.name = 'SchemaCycleError';
    this.tables = tables;
  }
}

export class UnknownTableError extends Error {
  constructor(table: string) {
    super(`Unknown table: ${table}`);
    this.name = 'UnknownTableError';
  }
}

export class SchemaGraph {
  private readonly tables = new Map<string, TableSchema>();
  private readonly parentEdges = new Map<string, Set<string>>();
  private readonly childEdges = new Map<string, Set<string>>();
  private readonly insertLevels = new Map<string, number>();

  constructor(definition: SchemaDefinition) {
    for (const table of definition) {
      if (this.tables.has(table.name)) {
        throw new Error(`Duplicate table definition: ${table.name}`);
      }
      this.tables.set(table.name, table);
      this.parentEdges.set(table.name, new Set());
      this.childEdges.set(table.name, new Set());
    }
    for (const table of definition) {
      for (const fk of table.foreignKeys) {
        const parent = fk.references.table;
        if (!this.tables.has(parent)) {
          throw new UnknownTableError(parent);
        }
        this.parentEdges.get(table.name)!.add(parent);
        this.childEdges.get(parent)!.add(table.name);
      }
    }
    this.insertLevels = this.computeLevels();
  }

  table(name: string): TableSchema {
    const table = this.tables.get(name);
    if (!table) {
      throw new UnknownTableError(name);
    }
    return table;
  }

  hasTable(name: string): boolean {
    return this.tables.has(name);
  }

  tableNames(): string[] {
    return [...this.tables.keys()];
  }

  parentsOf(name: string): string[] {
    this.assertKnown(name);
    return [...this.parentEdges.get(name)!];
  }

  childrenOf(name: string): string[] {
    this.assertKnown(name);
    return [...this.childEdges.get(name)!];
  }

  foreignKeysOf(name: string): ForeignKey[] {
    return this.table(name).foreignKeys;
  }

  topoOrder(): string[] {
    return [...this.insertLevels.entries()]
      .sort((a, b) => a[1] - b[1])
      .map(([name]) => name);
  }

  deleteOrder(): string[] {
    return this.topoOrder().reverse();
  }

  insertLevel(name: string): number {
    this.assertKnown(name);
    return this.insertLevels.get(name)!;
  }

  private computeLevels(): Map<string, number> {
    const indegree = new Map<string, number>();
    for (const [name, parents] of this.parentEdges) {
      indegree.set(name, parents.size);
    }
    const levels = new Map<string, number>();
    let frontier = [...indegree.entries()]
      .filter(([, degree]) => degree === 0)
      .map(([name]) => name);
    let level = 0;
    let placed = 0;
    while (frontier.length > 0) {
      const next: string[] = [];
      for (const name of frontier.sort()) {
        levels.set(name, level);
        placed += 1;
        for (const child of this.childEdges.get(name)!) {
          const remaining = indegree.get(child)! - 1;
          indegree.set(child, remaining);
          if (remaining === 0) {
            next.push(child);
          }
        }
      }
      frontier = next;
      level += 1;
    }
    if (placed < this.tables.size) {
      const remaining = [...indegree.entries()]
        .filter(([, degree]) => degree > 0)
        .map(([name]) => name);
      throw new SchemaCycleError(remaining);
    }
    return levels;
  }

  private assertKnown(name: string): void {
    if (!this.tables.has(name)) {
      throw new UnknownTableError(name);
    }
  }
}
