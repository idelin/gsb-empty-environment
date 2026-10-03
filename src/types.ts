/** A row of table data keyed by column name. */
export type Row = Record<string, unknown>;

/** CDC operation kinds. */
export type Op = "insert" | "update" | "delete";

/**
 * A single CDC change event.
 * `seq` is the arrival order into the scheduler, which may violate
 * foreign-key dependency order (e.g. an OrderItem insert arriving
 * before the User insert it transitively depends on).
 */
export interface ChangeEvent {
  /** Unique event id, used in traces and assertions. */
  id: string;
  /** Arrival sequence number (out-of-order with respect to FK deps). */
  seq: number;
  table: string;
  op: Op;
  /**
   * insert/update: the full after-image row.
   * delete: at least the primary key column.
   */
  row: Row;
}

/** A foreign key: childTable.childColumn -> parentTable.parentColumn. */
export interface ForeignKey {
  name: string;
  childTable: string;
  childColumn: string;
  parentTable: string;
  parentColumn: string;
}

export interface TableDef {
  name: string;
  primaryKey: string;
}
