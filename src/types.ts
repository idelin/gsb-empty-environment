export type Value = number | string | boolean | null;

export type CompareOp = 'eq' | 'neq' | 'lt' | 'lte' | 'gt' | 'gte';

export interface Predicate {
  col: string;
  op: CompareOp;
  value: Value;
}

export interface SortSpec {
  key: string;
  dir?: 'asc' | 'desc';
}

export interface Plan {
  filter?: Predicate;
  sort?: SortSpec;
  project: string[];
}

export interface ResultRow {
  rowId: number;
  rowVersion: number;
  values: Value[];
}

export interface QueryResult {
  columns: string[];
  rows: ResultRow[];
}
