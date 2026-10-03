/**
 * Catalog: table & index metadata. An index on (c1, c2, ...) means an
 * IndexScan over it delivers rows ordered by those columns (ascending).
 */

export interface IndexDef {
  name: string;
  columns: string[]; // unqualified column names, in index key order
}

export interface TableDef {
  name: string;
  indexes: IndexDef[];
}

export class Catalog {
  private tables = new Map<string, TableDef>();

  addTable(table: TableDef): this {
    this.tables.set(table.name, table);
    return this;
  }

  getTable(name: string): TableDef {
    const table = this.tables.get(name);
    if (!table) throw new Error(`unknown table: ${name}`);
    return table;
  }
}

/** Demo catalog: users has a B-tree on (id) and (id, name); orders has none. */
export function demoCatalog(): Catalog {
  return new Catalog()
    .addTable({
      name: 'users',
      indexes: [
        { name: 'users_pkey', columns: ['id'] },
        { name: 'users_id_name_idx', columns: ['id', 'name'] },
      ],
    })
    .addTable({
      name: 'orders',
      indexes: [{ name: 'orders_pkey', columns: ['id'] }],
    });
}
