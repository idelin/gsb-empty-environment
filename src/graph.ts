import type { ForeignKey, TableDef } from "./types.ts";

/**
 * Foreign-key dependency graph over tables.
 *
 * Edge direction: child -> parent ("child depends on parent").
 * - INSERT/UPDATE must be applied in topological order (parents first).
 * - DELETE must be applied in reverse topological order (children first).
 */
export class DependencyGraph {
  private tables = new Map<string, TableDef>();
  private fks: ForeignKey[] = [];

  addTable(def: TableDef): this {
    if (this.tables.has(def.name)) {
      throw new Error(`duplicate table: ${def.name}`);
    }
    this.tables.set(def.name, def);
    return this;
  }

  addForeignKey(fk: ForeignKey): this {
    for (const t of [fk.childTable, fk.parentTable]) {
      if (!this.tables.has(t)) throw new Error(`unknown table in FK ${fk.name}: ${t}`);
    }
    this.fks.push(fk);
    return this;
  }

  tableDef(name: string): TableDef {
    const def = this.tables.get(name);
    if (!def) throw new Error(`unknown table: ${name}`);
    return def;
  }

  hasTable(name: string): boolean {
    return this.tables.has(name);
  }

  /** FKs declared on `table` (its outgoing edges to parents). */
  foreignKeysOf(table: string): ForeignKey[] {
    return this.fks.filter((fk) => fk.childTable === table);
  }

  /** FKs pointing at `table` (incoming edges from children). */
  childKeysOf(table: string): ForeignKey[] {
    return this.fks.filter((fk) => fk.parentTable === table);
  }

  tableNames(): string[] {
    return [...this.tables.keys()];
  }

  /**
   * Kahn's algorithm: parents before children.
   * Throws on cyclic foreign keys.
   */
  topoOrder(): string[] {
    const indegree = new Map<string, number>();
    const dependents = new Map<string, string[]>();
    for (const name of this.tables.keys()) {
      indegree.set(name, 0);
      dependents.set(name, []);
    }
    for (const fk of this.fks) {
      if (fk.childTable === fk.parentTable) continue; // self-FK: row-level, not table-level
      indegree.set(fk.childTable, (indegree.get(fk.childTable) ?? 0) + 1);
      dependents.get(fk.parentTable)!.push(fk.childTable);
    }
    const queue = this.tableNames().filter((n) => indegree.get(n) === 0);
    const order: string[] = [];
    while (queue.length > 0) {
      const node = queue.shift()!;
      order.push(node);
      for (const child of dependents.get(node) ?? []) {
        const d = indegree.get(child)! - 1;
        indegree.set(child, d);
        if (d === 0) queue.push(child);
      }
    }
    if (order.length !== this.tables.size) {
      const remaining = this.tableNames().filter((n) => !order.includes(n));
      throw new Error(`foreign-key cycle detected among tables: ${remaining.join(", ")}`);
    }
    return order;
  }

  /** Children before parents: the order in which DELETEs are safe. */
  deleteOrder(): string[] {
    return this.topoOrder().reverse();
  }
}
