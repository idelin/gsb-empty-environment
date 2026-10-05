/** A constant value in an attribute domain. */
export type Value = number | string | boolean;

/** Schema of one base relation: attribute names plus a finite domain per attribute. */
export interface RelationSchema {
  name: string;
  attrs: string[];
  /** domains[i] is the finite domain of attrs[i]. */
  domains: Value[][];
}

/** A row maps attribute names to values. */
export type Row = Record<string, Value>;

/** A database instance: relation name -> set of rows (no duplicates). */
export type Database = Record<string, Row[]>;

/** Operand of a comparison: an attribute of the current tuple or a constant. */
export type Operand = { attr: string } | { const: Value };

/** Selection predicate. */
export type Predicate =
  | { op: 'eq' | 'ne' | 'lt' | 'le' | 'gt' | 'ge'; left: Operand; right: Operand }
  | { op: 'and' | 'or'; args: Predicate[] }
  | { op: 'not'; arg: Predicate };

/** Relational algebra query over selection, projection, union and difference. */
export type Query =
  | { kind: 'relation'; name: string }
  | { kind: 'select'; where: Predicate; input: Query }
  | { kind: 'project'; attrs: string[]; input: Query }
  | { kind: 'union'; left: Query; right: Query }
  | { kind: 'difference'; left: Query; right: Query };
