export type AggregationType = 'sum' | 'count' | 'avg' | 'min' | 'max';

export interface MeasureConfig {
  field: string;
  aggregation: AggregationType;
  label?: string;
}

export type SortComparator = (a: string, b: string) => number;

export type SortSpec = 'asc' | 'desc' | SortComparator;

export interface PivotConfig {
  rows: string[];
  columns: string[];
  measures: MeasureConfig[];
  blankLabel?: string;
  subtotalLabel?: string;
  grandTotalLabel?: string;
  sorts?: { [field: string]: SortSpec };
}

export interface DataRecord {
  id?: string | number;
  [field: string]: unknown;
}

export interface MeasureMeta {
  field: string;
  aggregation: AggregationType;
  label: string;
}

export interface PivotColumn {
  path: string[];
  isSubtotal: boolean;
  isGrandTotal: boolean;
  level: number;
}

export interface PivotRow {
  headers: (string | null)[];
  path: string[];
  isSubtotal: boolean;
  isGrandTotal: boolean;
  level: number;
  cells: (number | null)[][];
}

export interface PivotResult {
  measures: MeasureMeta[];
  columns: PivotColumn[];
  rows: PivotRow[];
  grandTotal: (number | null)[];
}
