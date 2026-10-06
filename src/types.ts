export type TimeKey = number | string;

export type StackMode = 'absolute' | 'percent';

export interface DataPoint {
  time: TimeKey;
  /**
   * Raw value. `null` means the series explicitly reported this time as
   * missing; it is distinct from `0`, which is a real measurement.
   */
  value: number | null;
}

export interface SeriesInput {
  id: string;
  points: readonly DataPoint[];
}

export interface StackOptions {
  mode: StackMode;
  /** Series ids excluded from stacking without being removed from the input. */
  hidden?: ReadonlySet<string>;
}

export interface StackedPoint {
  time: TimeKey;
  seriesId: string;
  /** Raw value as reported; `null` when missing. */
  value: number | null;
  /** True when the series has no usable value at this time. */
  missing: boolean;
  /** Lower stack boundary (normalized in percent mode). */
  lower: number;
  /** Upper stack boundary (normalized in percent mode). */
  upper: number;
}

export interface StackedColumn {
  time: TimeKey;
  points: StackedPoint[];
  /** Sum of positive raw values in this column. */
  posTotal: number;
  /** Sum of absolute negative raw values in this column. */
  negTotal: number;
  /** Top of the positive stack (final positive cursor). */
  posExtent: number;
  /** Bottom of the negative stack (final negative cursor). */
  negExtent: number;
}

export interface StackResult {
  mode: StackMode;
  times: TimeKey[];
  seriesIds: string[];
  columns: StackedColumn[];
}
