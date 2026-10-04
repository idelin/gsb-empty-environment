export type Value = string | number | boolean | null;

export type TriState = 'true' | 'false' | 'unknown';

export type CompareOp = '=' | '!=' | '<' | '<=' | '>' | '>=';

export interface FilterSpec {
  column: string;
  op: CompareOp;
  value: Value;
}

export interface SortSpec {
  column: string;
  direction?: 'asc' | 'desc';
}

export interface QueryPlan {
  filter?: FilterSpec;
  sort?: SortSpec;
  project: string[];
}

export interface RowRef {
  batchId: number;
  rowIndex: number;
  rowId: number;
  version: number;
}

export interface ResultRow {
  rowId: number;
  version: number;
  values: Record<string, Value>;
}
