export interface Run {
  label: number;
  length: number;
}

export interface RowChange {
  row: number;
  before: Run[];
  after: Run[];
}

export interface BaselineSummary {
  width: number;
  height: number;
  totalPixels: number;
  blockPixels: number[];
  checksum: number;
}

export interface Patch {
  seq: number;
  kind: string;
  changes: RowChange[];
  baseline: BaselineSummary;
  result: BaselineSummary;
}

export interface Snapshot {
  id: number;
  seq: number;
  rows: Run[][];
  blockPixels: number[];
  summary: BaselineSummary;
}
